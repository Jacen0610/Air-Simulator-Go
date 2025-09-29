// C:/workspace/go/Air-Simulator-Go/simulation/center.go
package simulation

import (
	"Air-Simulator/config"
	"fmt"
	"log"
	"math/rand"
	"sync/atomic"
	"time"
)

// GroundControlCenter 代表一个地面控制站。
type GroundControlCenter struct {
	ID           string
	inboundQueue chan ACARSMessageInterface // 自己的内部消息队列
	outbox       chan OutboxItem            // 自己的消息发件箱
	metricsChan  chan<- time.Duration       // 用于发送指标的通道 (只写)

	// --- 通信统计 ---
	totalTxAttempts   uint64       // 总传输尝试次数 (每次尝试获得信道)
	totalCollisions   uint64       // 碰撞/信道访问失败次数
	successfulTx      uint64       // 成功发送并收到ACK的报文总数
	totalRqTunnel     uint64       // 总请求隧道次数
	totalFailRqTunnel uint64       // 失败请求隧道次数
	totalWaitTimeNs   atomic.Int64 // 总等待时间 (纳秒)
}

// NewGroundControlCenter 是 GroundControlCenter 的构造函数。
func NewGroundControlCenter(id string, metricsChan chan<- time.Duration) *GroundControlCenter {
	return &GroundControlCenter{
		ID:           id,
		inboundQueue: make(chan ACARSMessageInterface, 50), // 为其分配一个带缓冲的队列
		outbox:       make(chan OutboxItem, 100),           // 初始化发件箱
		metricsChan:  metricsChan,
	}
}

// EnqueueMessage 将消息放入发件箱以供发送
func (gcc *GroundControlCenter) EnqueueMessage(msg ACARSMessageInterface) {
	item := OutboxItem{
		Message:     msg,
		EnqueueTime: time.Now(),
	}
	gcc.outbox <- item
	log.Printf("📥 [地面站 %s] 新报文 (ID: %s) 已加入发件箱。", gcc.ID, msg.GetBaseMessage().MessageID)
}

// ProcessOutbox 循环并串行处理发件箱中的消息
func (gcc *GroundControlCenter) ProcessOutbox(comms *CommunicationSystem) {
	for item := range gcc.outbox {
		// 确保一次只发送一封邮件，因此这里是同步调用
		gcc.SendMessage(item, comms)
	}
}

// StartListening 启动地面站的监听服务。
func (gcc *GroundControlCenter) StartListening(commsSystem *CommunicationSystem) {
	commsSystem.RegisterListener(gcc.inboundQueue)
	log.Printf("🛰️  地面站 [%s] 已启动，开始监听通信系统...", gcc.ID)

	go gcc.ProcessOutbox(commsSystem) // 启动发件箱处理器

	for msg := range gcc.inboundQueue {
		go gcc.processMessage(msg, commsSystem)
	}
}

// processMessage 是内部处理方法，处理单个报文并发送 ACK。
func (gcc *GroundControlCenter) processMessage(msg ACARSMessageInterface, commsSystem *CommunicationSystem) {
	baseMsg := msg.GetBaseMessage()

	if baseMsg.AircraftICAOAddress == gcc.ID {
		return
	}

	// 模拟处理延迟
	time.Sleep(config.ProcessingDelay)

	log.Printf("✅ [%s] 报文 %s 处理完毕，准备发送高优先级 ACK...", gcc.ID, baseMsg.MessageID)

	// 创建 ACK 报文
	ackData := AcknowledgementData{
		OriginalMessageID: baseMsg.MessageID,
		Status:            "RECEIVED",
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

// SendMessage 使用 p-坚持 CSMA 算法在选定的信道上发送报文。
func (gcc *GroundControlCenter) SendMessage(item OutboxItem, commsSystem *CommunicationSystem) {
	msg := item.Message
	enqueueTime := item.EnqueueTime
	baseMsg := msg.GetBaseMessage()

	targetChannel := commsSystem.SelectChannelForMessage(msg, gcc.ID)
	p := targetChannel.GetPForMessage(msg.GetPriority())
	timeSlotForChannel := targetChannel.GetCurrentTimeSlot()

	// 地面站目前只发送ACK，所以日志可以保持具体
	log.Printf("🚀 [%s] 准备发送 ACK (ID: %s, Prio: %s)", gcc.ID, baseMsg.MessageID, msg.GetPriority())

	// 地面站将持续尝试发送 ACK 直到成功
	for {
		// [核心修改 2] 发送前检查：在每次尝试发送前，都检查ACK是否已“陈旧”。
		// 如果因为信道持续拥堵，导致这个ACK从入队(enqueueTime)到当前(Now)的等待时间
		// 已经超过了飞机的超时阈值，就直接放弃发送，以避免浪费资源并处理下一个消息。
		if time.Since(enqueueTime) > config.AckTimeout {
			log.Printf("🗑️  [地面站 %s] 放弃发送陈旧的ACK (for msg: %s)，因信道拥堵等待时间过长。", gcc.ID, baseMsg.MessageID)
			return // 放弃发送，ProcessOutbox将处理下一个消息
		}

		atomic.AddUint64(&gcc.totalRqTunnel, 1)

		if !targetChannel.IsBusy() {
			if rand.Float64() < p {
				atomic.AddUint64(&gcc.totalTxAttempts, 1)

				if targetChannel.AttemptTransmit(msg, gcc.ID, config.TransmissionTime) {
					waitTime := time.Since(enqueueTime)
					gcc.totalWaitTimeNs.Add(waitTime.Nanoseconds())
					atomic.AddUint64(&gcc.successfulTx, 1)

					if gcc.metricsChan != nil {
						select {
						case gcc.metricsChan <- waitTime:
						default:
							log.Printf("⚠️ [地面站 %s] 指标通道已满，本次耗时 %v 未能记录", gcc.ID, waitTime)
						}
					}

					log.Printf("✅ [%s] 在信道 [%s] 上成功发送 ACK (ID: %s), 耗时: %v", gcc.ID, targetChannel.ID, baseMsg.MessageID, waitTime)
					return // 成功发送后退出函数
				} else {
					atomic.AddUint64(&gcc.totalCollisions, 1)
					log.Printf("💥 [%s] 在信道 [%s] 上发送 ACK 时发生碰撞！", gcc.ID, targetChannel.ID)
				}
			} else {
				log.Printf("🤔 [%s] 信道 [%s] 空闲，但决定延迟发送 ACK (p=%.2f)...", gcc.ID, targetChannel.ID, p)
			}
		} else {
			atomic.AddUint64(&gcc.totalFailRqTunnel, 1)
			log.Printf("⏳ [%s] 发现信道 [%s] 忙，等待发送 ACK...", gcc.ID, targetChannel.ID)
		}

		time.Sleep(timeSlotForChannel)
	}
}

// ResetStats 重置所有统计计数器。
func (gcc *GroundControlCenter) ResetStats() {
	atomic.StoreUint64(&gcc.totalTxAttempts, 0)
	atomic.StoreUint64(&gcc.totalCollisions, 0)
	atomic.StoreUint64(&gcc.successfulTx, 0)
	atomic.StoreUint64(&gcc.totalRqTunnel, 0)
	atomic.StoreUint64(&gcc.totalFailRqTunnel, 0)
	gcc.totalWaitTimeNs.Store(0)
}

// GroundControlRawStats 定义了用于数据收集的原始统计数据结构。
type GroundControlRawStats struct {
	SuccessfulTx      uint64
	TotalTxAttempts   uint64
	TotalCollisions   uint64
	TotalRqTunnel     uint64
	TotalFailRqTunnel uint64
	TotalWaitTimeNs   time.Duration
}

// GetRawStats 返回原始统计数据，用于写入报告。
func (gcc *GroundControlCenter) GetRawStats() GroundControlRawStats {
	return GroundControlRawStats{
		SuccessfulTx:      atomic.LoadUint64(&gcc.successfulTx),
		TotalTxAttempts:   atomic.LoadUint64(&gcc.totalTxAttempts),
		TotalCollisions:   atomic.LoadUint64(&gcc.totalCollisions),
		TotalRqTunnel:     atomic.LoadUint64(&gcc.totalRqTunnel),
		TotalFailRqTunnel: atomic.LoadUint64(&gcc.totalFailRqTunnel),
		TotalWaitTimeNs:   time.Duration(gcc.totalWaitTimeNs.Load()),
	}
}
