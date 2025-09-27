package simulation

import (
	"Air-Simulator/config"
	"fmt"
	"log"
	"math"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// GroundControlCenter 代表一个地面控制站。
type GroundControlCenter struct {
	ID            string
	inboundQueue  chan ACARSMessageInterface
	outboundQueue []outboxItem // 发送队列
	outboundMutex sync.RWMutex
	pendingReward atomic.Int64

	// --- 通信统计 ---
	totalTxAttempts   uint64
	totalCollisions   uint64
	successfulTx      uint64
	totalRqTunnel     uint64
	totalFailRqTunnel uint64
	totalWaitTimeNs   atomic.Int64
}

// NewGroundControlCenter 是 GroundControlCenter 的构造函数。
func NewGroundControlCenter(id string) *GroundControlCenter {
	return &GroundControlCenter{
		ID:            id,
		inboundQueue:  make(chan ACARSMessageInterface, 50),
		outboundQueue: make([]outboxItem, 0, 20),
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

// EnqueueMessage 将新消息放入发件箱。
func (gcc *GroundControlCenter) EnqueueMessage(msg ACARSMessageInterface) {
	gcc.outboundMutex.Lock()
	defer gcc.outboundMutex.Unlock()

	item := outboxItem{
		message:          msg,
		enqueueTime:      time.Now(),
		isRetransmission: false, // 地面站不重传，此项总为 false
	}
	gcc.outboundQueue = append(gcc.outboundQueue, item)
}

// peekMessage 查看（不移除）队列头部的消息。
func (gcc *GroundControlCenter) peekMessage() *outboxItem {
	gcc.outboundMutex.RLock()
	defer gcc.outboundMutex.RUnlock()
	if len(gcc.outboundQueue) == 0 {
		return nil
	}
	return &gcc.outboundQueue[0]
}

// removeMessageFromQueue 从队列中移除消息。
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
		PendingAcksCount:    0, // 地面站不等待ACK
		OutboundQueueLength: int32(queueLen),
	}
	if comms.BackupChannel != nil {
		obs.BackupChannelBusy = comms.BackupChannel.IsBusy()
	}

	if topItem := gcc.peekMessage(); topItem != nil {
		obs.HasMessage = true
		obs.TopMessageWaitTimeSeconds = float32(time.Since(topItem.enqueueTime).Seconds())
		obs.IsRetransmission = topItem.isRetransmission
	} else {
		obs.HasMessage = false
		obs.TopMessageWaitTimeSeconds = 0
		obs.IsRetransmission = false
	}

	return obs
}

// Step 是地面站 MARL 模式下的核心执行函数。
func (gcc *GroundControlCenter) Step(action AgentAction, comms *CommunicationSystem) float32 {
	reward := float32(0.0)

	// 1. 清理过期的 ACK 报文
	gcc.outboundMutex.Lock()
	i := 0
	for i < len(gcc.outboundQueue) {
		item := gcc.outboundQueue[i]
		if time.Since(item.enqueueTime) > config.AckTimeout {
			log.Printf("🗑️ [地面站 %s] 丢弃过期ACK (ID: %s)，因其已在队列中停留过久。", gcc.ID, item.message.GetBaseMessage().MessageID)
			reward -= 25.0
			gcc.outboundQueue = append(gcc.outboundQueue[:i], gcc.outboundQueue[i+1:]...)
		} else {
			i++
		}
	}
	// 按入队时间排序，确保 FIFO
	sort.Slice(gcc.outboundQueue, func(i, j int) bool {
		return gcc.outboundQueue[i].enqueueTime.Before(gcc.outboundQueue[j].enqueueTime)
	})
	gcc.outboundMutex.Unlock()

	// 2. 根据动作执行决策
	itemToSend := gcc.peekMessage()
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
		if comms.PrimaryChannel.IsBusy() && (comms.BackupChannel == nil || comms.BackupChannel.IsBusy()) {
			reward -= 0.5
		} else {
			waitTime := float32(time.Since(itemToSend.enqueueTime).Seconds())
			penalty := 2.0 + waitTime*2.5
			reward -= penalty
		}

	case ActionSendPrimary:
		reward += gcc.attemptSendOnChannel(itemToSend, comms.PrimaryChannel)
	case ActionSendBackup:
		if comms.BackupChannel != nil {
			reward += gcc.attemptSendOnChannel(itemToSend, comms.BackupChannel)
		} else {
			reward -= 15.0
		}
	}

	return reward
}

// attemptSendOnChannel 尝试在指定信道上发送消息
func (gcc *GroundControlCenter) attemptSendOnChannel(item *outboxItem, channel *Channel) float32 {
	atomic.AddUint64(&gcc.totalRqTunnel, 1)
	time.Sleep(time.Duration(10+rand.Intn(41)) * time.Microsecond)

	if channel.IsBusy() {
		atomic.AddUint64(&gcc.totalFailRqTunnel, 1)
		return -1.0
	}

	atomic.AddUint64(&gcc.totalTxAttempts, 1)

	if channel.AttemptTransmit(item.message, gcc.ID, config.TransmissionTime) {
		waitTime := time.Since(item.enqueueTime)
		gcc.totalWaitTimeNs.Add(waitTime.Nanoseconds())
		gcc.removeMessageFromQueue(item.message.GetBaseMessage().MessageID)
		atomic.AddUint64(&gcc.successfulTx, 1)
		log.Printf("✅ [地面站 %s] 成功抢占信道并发送 ACK (ID: %s)。排队等待时间: %s", gcc.ID, item.message.GetBaseMessage().MessageID, waitTime)

		// [核心修改] 使用指数衰减函数计算奖励
		const maxReward = 20.0
		const benchmarkTime = 0.4 // 400ms
		const decayConstant = 5.0

		waitTimeSeconds := waitTime.Seconds()
		var reward float32
		if waitTimeSeconds > benchmarkTime {
			excessTime := waitTimeSeconds - benchmarkTime
			reward = float32(maxReward * math.Exp(-decayConstant*excessTime))
		} else {
			// 如果实际等待时间小于等于理论极限，给予最大奖励
			reward = maxReward
		}
		return max(reward, float32(1.0))
	} else {
		atomic.AddUint64(&gcc.totalCollisions, 1)
		log.Printf("💥 [地面站 %s] 发送 ACK (ID: %s) 时失败(碰撞)。", gcc.ID, item.message.GetBaseMessage().MessageID)
		return -50.0
	}
}

// Reset 重置所有统计计数器。
func (gcc *GroundControlCenter) Reset() {
	atomic.StoreUint64(&gcc.totalTxAttempts, 0)
	atomic.StoreUint64(&gcc.totalCollisions, 0)
	atomic.StoreUint64(&gcc.successfulTx, 0)
	atomic.StoreUint64(&gcc.totalRqTunnel, 0)
	atomic.StoreUint64(&gcc.totalFailRqTunnel, 0)
	gcc.totalWaitTimeNs.Store(0)

	gcc.outboundMutex.Lock()
	gcc.outboundQueue = make([]outboxItem, 0, 20)
	gcc.outboundMutex.Unlock()
}

// GroundControlRawStats 定义了用于数据收集的原始统计数据结构。
type GroundControlRawStats struct {
	SuccessfulTx      uint64
	TotalTxAttempts   uint64
	TotalCollisions   uint64
	TotalRqTunnel     uint64
	TotalFailRqTunnel uint64
	TotalWaitTime     time.Duration
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
		TotalWaitTime:     time.Duration(gcc.totalWaitTimeNs.Load()),
		UnsentMessages:    unsentMessage,
	}
}
