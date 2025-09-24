// C:/workspace/go/Air-Simulator-Go/simulation/center.go
package simulation

import (
	"Air-Simulator/config"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// GroundControlCenter 代表一个地面控制站。
type GroundControlCenter struct {
	ID            string
	inboundQueue  chan ACARSMessageInterface // 自己的内部消息队列
	outboundQueue []outboxItem               // **[修改]** 发送队列，现在存储 outboxItem
	outboundMutex sync.RWMutex               // 保护队列的锁
	pendingReward atomic.Int64

	// --- 通信统计 ---
	totalTxAttempts   uint64       // 总传输尝试次数 (每次调用 AttemptTransmit)
	totalCollisions   uint64       // 碰撞/信道访问失败次数
	successfulTx      uint64       // 成功启动传输的报文总数
	totalRqTunnel     uint64       // 总请求隧道次数 (在 MARL 模式下未使用)
	totalFailRqTunnel uint64       // 失败请求隧道次数 (在 MARL 模式下未使用)
	totalWaitTimeNs   atomic.Int64 // **[新增]** 总等待时间 (纳秒)
}

// NewGroundControlCenter 是 GroundControlCenter 的构造函数。
func NewGroundControlCenter(id string) *GroundControlCenter {
	return &GroundControlCenter{
		ID:            id,
		inboundQueue:  make(chan ACARSMessageInterface, 50),
		outboundQueue: make([]outboxItem, 0, 20), // **[修改]** 初始化发件箱
	}
}

// StartListening 启动地面站的监听服务。
func (gcc *GroundControlCenter) StartListening(commsSystem *CommunicationSystem) {
	commsSystem.RegisterListener(gcc.inboundQueue)
	log.Printf("🛰️  地面站 [%s] 已启动，开始监听通信系统...", gcc.ID)

	for msg := range gcc.inboundQueue {
		go gcc.processMessage(msg)
	}
}

// processMessage 处理收到的消息并准备ACK。
func (gcc *GroundControlCenter) processMessage(msg ACARSMessageInterface) {
	baseMsg := msg.GetBaseMessage()
	if baseMsg.AircraftICAOAddress == gcc.ID {
		return
	}

	time.Sleep(config.ProcessingDelay)

	ackData := AcknowledgementData{
		OriginMessagePriority: msg.GetPriority(),
		OriginalMessageID:     baseMsg.MessageID,
		Status:                "RECEIVED",
	}
	ackBaseMsg := ACARSBaseMessage{
		AircraftICAOAddress: gcc.ID,
		FlightID:            "GND_CTL",
		MessageID:           fmt.Sprintf("ACK-%s", baseMsg.MessageID),
		Timestamp:           time.Now(),
		Type:                MsgTypeAck,
	}

	ackMessage, err := NewCriticalPriorityMessage(ackBaseMsg, ackData)
	if err != nil {
		log.Printf("错误: [%s] 创建 ACK 报文失败: %v", gcc.ID, err)
		return
	}

	gcc.EnqueueMessage(ackMessage)
}

// EnqueueMessage 将新消息放入发件箱，并记录其入队时间。
func (gcc *GroundControlCenter) EnqueueMessage(msg ACARSMessageInterface) {
	gcc.outboundMutex.Lock()
	defer gcc.outboundMutex.Unlock()

	item := outboxItem{
		message:     msg,
		enqueueTime: time.Now(),
	}
	gcc.outboundQueue = append(gcc.outboundQueue, item)

}

// peekHighestPriorityMessage 查看（不移除）最重要的消息条目。
func (gcc *GroundControlCenter) peekHighestPriorityMessage() *outboxItem {
	gcc.outboundMutex.RLock()
	defer gcc.outboundMutex.RUnlock()
	if len(gcc.outboundQueue) == 0 {
		return nil
	}
	return &gcc.outboundQueue[0]
}

// removeMessageFromQueue 在消息成功启动传输后将其从队列中移除。
func (gcc *GroundControlCenter) removeMessageFromQueue(messageID string) {
	gcc.outboundMutex.Lock()
	defer gcc.outboundMutex.Unlock()
	for i, item := range gcc.outboundQueue {
		if item.message.GetBaseMessage().MessageID == messageID {
			gcc.outboundQueue = append(gcc.outboundQueue[:i], gcc.outboundQueue[i+1:]...)
			return
		}
	}
}

// GetObservation 为地面站 MARL 代理生成当前的观测数据。
func (gcc *GroundControlCenter) GetObservation(comms *CommunicationSystem) AgentObservation {
	gcc.outboundMutex.RLock()
	queueLen := len(gcc.outboundQueue)
	gcc.outboundMutex.RUnlock()

	obs := AgentObservation{
		PrimaryChannelBusy:  comms.PrimaryChannel.IsBusy(),
		PendingAcksCount:    int32(0),
		OutboundQueueLength: int32(queueLen),
	}
	if comms.BackupChannel != nil {
		obs.BackupChannelBusy = comms.BackupChannel.IsBusy()
	}

	if topItem := gcc.peekHighestPriorityMessage(); topItem != nil {
		obs.HasMessage = true
		// **[修改]** 从 item 中获取消息优先级
		obs.TopMessagePriority = topItem.message.GetPriority()
	} else {
		obs.HasMessage = false
	}

	return obs
}

// Step 是地面站 MARL 模式下的核心执行函数。
func (gcc *GroundControlCenter) Step(action AgentAction, comms *CommunicationSystem) float32 {
	reward := float32(0.0)

	gcc.outboundMutex.Lock()
	i := 0
	for i < len(gcc.outboundQueue) {
		item := gcc.outboundQueue[i]
		if time.Since(item.enqueueTime) > config.AckTimeout {
			log.Printf("🗑️ [地面站 %s] 丢弃过期ACK (ID: %s)，因其已在队列中停留过久。", gcc.ID, item.message.GetBaseMessage().MessageID)
			reward -= 20.0
			gcc.outboundQueue = append(gcc.outboundQueue[:i], gcc.outboundQueue[i+1:]...)
		} else {
			i++
		}
	}
	sort.Slice(gcc.outboundQueue, func(i, j int) bool {
		return gcc.outboundQueue[i].enqueueTime.Before(gcc.outboundQueue[j].enqueueTime)
	})
	gcc.outboundMutex.Unlock()

	itemToSend := gcc.peekHighestPriorityMessage()
	if itemToSend == nil {
		if action == ActionSendPrimary || action == ActionSendBackup {
			reward -= 10.0
		} else {
			reward += 1.0
		}
		return reward
	}

	switch action {
	case ActionWait:
		if comms.PrimaryChannel.IsBusy() && comms.BackupChannel != nil && comms.BackupChannel.IsBusy() {
			reward += 0.5
		} else {
			gcc.outboundMutex.RLock()
			queueLen := len(gcc.outboundQueue)
			gcc.outboundMutex.RUnlock()

			var originalPriorityValue int
			if rawData, ok := itemToSend.message.GetData().(json.RawMessage); ok {
				var ackData AcknowledgementData
				if json.Unmarshal(rawData, &ackData) == nil {
					originalPriorityValue = ackData.OriginMessagePriority.Value()
				}
			}

			penalty := 1.0 + (float32(queueLen) * 1) + (float32(originalPriorityValue) * 0.2)
			reward -= penalty
		}
	case ActionSendPrimary:
		reward += gcc.attemptSendOnChannel(itemToSend, comms.PrimaryChannel)
	case ActionSendBackup:
		if comms.BackupChannel != nil {
			reward += gcc.attemptSendOnChannel(itemToSend, comms.BackupChannel)
		} else {
			reward -= 10.0
		}
	}

	return reward
}

// **[核心修改]** attemptSendOnChannel 现在使用同步阻塞模型并计算等待时间。
func (gcc *GroundControlCenter) attemptSendOnChannel(item *outboxItem, channel *Channel) float32 {
	atomic.AddUint64(&gcc.totalRqTunnel, 1)
	time.Sleep(time.Duration(10+rand.Intn(41)) * time.Microsecond)
	if channel.IsBusy() {
		atomic.AddUint64(&gcc.totalFailRqTunnel, 1)
		return -2.0
	}
	atomic.AddUint64(&gcc.totalTxAttempts, 1)

	// 调用新的同步阻塞 AttemptTransmit 函数
	if channel.AttemptTransmit(item.message, gcc.ID, config.TransmissionTime) {
		// **[核心修改]** 传输成功，计算并累加等待时间
		waitTime := time.Since(item.enqueueTime)
		gcc.totalWaitTimeNs.Add(waitTime.Nanoseconds())

		// 从队列中移除
		gcc.removeMessageFromQueue(item.message.GetBaseMessage().MessageID)
		atomic.AddUint64(&gcc.successfulTx, 1)

		log.Printf("✅ [地面站 %s] 成功抢占信道并发送 ACK (ID: %s)。排队等待时间: %s", gcc.ID, item.message.GetBaseMessage().MessageID, waitTime)

		return 5.0 // 成功发送的奖励
	} else {
		// 发生碰撞或信道被占用
		atomic.AddUint64(&gcc.totalCollisions, 1)
		log.Printf("💥 [地面站 %s] 发送 ACK (ID: %s) 时失败(碰撞或信道忙)。", gcc.ID, item.message.GetBaseMessage().MessageID)
		return -10.0 // 碰撞或失败的惩罚
	}
}

// Reset重置所有统计计数器。
func (gcc *GroundControlCenter) Reset() {
	atomic.StoreUint64(&gcc.totalTxAttempts, 0)
	atomic.StoreUint64(&gcc.totalCollisions, 0)
	atomic.StoreUint64(&gcc.successfulTx, 0)
	atomic.StoreUint64(&gcc.totalRqTunnel, 0)
	atomic.StoreUint64(&gcc.totalFailRqTunnel, 0)
	gcc.totalWaitTimeNs.Store(0)

	gcc.outboundMutex.Lock()
	gcc.outboundQueue = make([]outboxItem, 0, 20) // **[修改]**
	gcc.outboundMutex.Unlock()
}

// GroundControlRawStats 定义了用于数据收集的原始统计数据结构。
type GroundControlRawStats struct {
	SuccessfulTx      uint64
	TotalTxAttempts   uint64
	TotalCollisions   uint64
	TotalRqTunnel     uint64        // 在 MARL 模式下未使用
	TotalFailRqTunnel uint64        // 在 MARL 模式下未使用
	TotalWaitTime     time.Duration // **[修改]** 字段名统一
	UnsentMessages    int
}

// GetRawStats 返回原始统计数据，用于写入报告。
func (gcc *GroundControlCenter) GetRawStats() GroundControlRawStats {
	gcc.outboundMutex.RLock()
	unsentMessage := len(gcc.outboundQueue)
	gcc.outboundMutex.RUnlock()
	return GroundControlRawStats{
		SuccessfulTx:      atomic.LoadUint64(&gcc.successfulTx),
		TotalTxAttempts:   atomic.LoadUint64(&gcc.totalTxAttempts),
		TotalCollisions:   atomic.LoadUint64(&gcc.totalCollisions),
		TotalRqTunnel:     atomic.LoadUint64(&gcc.totalRqTunnel),
		TotalFailRqTunnel: atomic.LoadUint64(&gcc.totalFailRqTunnel),
		TotalWaitTime:     time.Duration(gcc.totalWaitTimeNs.Load()), // **[修改]**
		UnsentMessages:    unsentMessage,
	}
}
