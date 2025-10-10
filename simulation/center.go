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
	ID                   string
	inboundQueue         chan ACARSMessageInterface
	outbox               chan OutboxItem
	metricsChan          chan<- time.Duration
	totalTxAttempts      uint64
	totalCollisions      uint64
	successfulTx         uint64
	totalDroppedMessages uint64
	totalRqTunnel        uint64
	totalFailRqTunnel    uint64
	totalWaitTimeNs      atomic.Int64
}

// NewGroundControlCenter 是 GroundControlCenter 的构造函数。
func NewGroundControlCenter(id string, metricsChan chan<- time.Duration) *GroundControlCenter {
	return &GroundControlCenter{
		ID:           id,
		inboundQueue: make(chan ACARSMessageInterface, 50),
		outbox:       make(chan OutboxItem, 100),
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
		gcc.SendMessage(item, comms)
	}
}

// StartListening 启动地面站的监听服务。
func (gcc *GroundControlCenter) StartListening(commsSystem *CommunicationSystem) {
	commsSystem.RegisterListener(gcc.inboundQueue)
	log.Printf("🛰️  地面站 [%s] 已启动，开始监听通信系统...", gcc.ID)

	go gcc.ProcessOutbox(commsSystem)

	for msg := range gcc.inboundQueue {
		go gcc.processMessage(msg, commsSystem)
	}
}

// processMessage 是内部处理方法，处理单个报文并发送 ACK。
func (gcc *GroundControlCenter) processMessage(msg ACARSMessageInterface, commsSystem *CommunicationSystem) {
	baseMsg := msg.GetBaseMessage()

	// [核心修正] 检查并丢弃来自背景流量生成器的消息
	if baseMsg.AircraftICAOAddress == "BG_TRAFFIC" {
		log.Printf("🗑️  [%s] 已丢弃来自背景流量生成器的虚拟消息 (ID: %s)", gcc.ID, baseMsg.MessageID)
		return
	}

	if baseMsg.AircraftICAOAddress == gcc.ID {
		return
	}

	time.Sleep(config.ProcessingDelay)

	log.Printf("✅ [%s] 报文 %s 处理完毕，准备发送高优先级 ACK...", gcc.ID, baseMsg.MessageID)

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
	p := config.CSMA_CHANNEL
	timeSlotForChannel := targetChannel.GetCurrentTimeSlot()

	log.Printf("🚀 [%s] 准备发送 ACK (ID: %s, Prio: %s)", gcc.ID, baseMsg.MessageID, msg.GetPriority())

	for {
		if time.Since(enqueueTime) > config.AckTimeout {
			log.Printf("🗑️  [地面站 %s] 放弃发送陈旧的ACK (for msg: %s)，因信道拥堵等待时间过长。", gcc.ID, baseMsg.MessageID)
			atomic.AddUint64(&gcc.totalDroppedMessages, 1)
			return
		}

		atomic.AddUint64(&gcc.totalRqTunnel, 1)

		if !targetChannel.IsBusy() {
			if rand.Float64() < p {
				atomic.AddUint64(&gcc.totalTxAttempts, 1)

				if targetChannel.AttemptTransmit(msg, gcc.ID, config.TransmissionTime) {
					waitTime := time.Since(enqueueTime)
					gcc.totalWaitTimeNs.Add(waitTime.Nanoseconds())
					atomic.AddUint64(&gcc.successfulTx, 1)

					//if gcc.metricsChan != nil {
					//	select {
					//	case gcc.metricsChan <- waitTime:
					//	default:
					//		log.Printf("⚠️ [地面站 %s] 指标通道已满，本次耗时 %v 未能记录", gcc.ID, waitTime)
					//	}
					//}

					log.Printf("✅ [%s] 在信道 [%s] 上成功发送 ACK (ID: %s), 耗时: %v", gcc.ID, targetChannel.ID, baseMsg.MessageID, waitTime)
					return
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
	atomic.StoreUint64(&gcc.totalDroppedMessages, 0)
	atomic.StoreUint64(&gcc.totalRqTunnel, 0)
	atomic.StoreUint64(&gcc.totalFailRqTunnel, 0)
	gcc.totalWaitTimeNs.Store(0)
}

// GroundControlRawStats 定义了用于数据收集的原始统计数据结构。
type GroundControlRawStats struct {
	SuccessfulTx         uint64
	TotalTxAttempts      uint64
	TotalCollisions      uint64
	TotalDroppedMessages uint64
	TotalRqTunnel        uint64
	TotalFailRqTunnel    uint64
	TotalWaitTimeNs      time.Duration
}

// GetRawStats 返回原始统计数据，用于写入报告。
func (gcc *GroundControlCenter) GetRawStats() GroundControlRawStats {
	return GroundControlRawStats{
		SuccessfulTx:         atomic.LoadUint64(&gcc.successfulTx),
		TotalTxAttempts:      atomic.LoadUint64(&gcc.totalTxAttempts),
		TotalCollisions:      atomic.LoadUint64(&gcc.totalCollisions),
		TotalDroppedMessages: atomic.LoadUint64(&gcc.totalDroppedMessages),
		TotalRqTunnel:        atomic.LoadUint64(&gcc.totalRqTunnel),
		TotalFailRqTunnel:    atomic.LoadUint64(&gcc.totalFailRqTunnel),
		TotalWaitTimeNs:      time.Duration(gcc.totalWaitTimeNs.Load()),
	}
}
