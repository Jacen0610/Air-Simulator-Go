// C:/workspace/go/Air-Simulator-Go/center.go
package simulation

import (
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

	// --- 通信统计 ---
	totalTxAttempts   uint64       // 总传输尝试次数 (每次尝试获得信道)
	totalCollisions   uint64       // 碰撞/信道访问失败次数
	successfulTx      uint64       // 成功发送并收到ACK的报文总数
	totalRqTunnel     uint64       // 总请求隧道次数
	totalFailRqTunnel uint64       // 失败请求隧道次数
	totalWaitTimeNs   atomic.Int64 // 总等待时间 (纳秒)
}

// NewGroundControlCenter 是 GroundControlCenter 的构造函数。
func NewGroundControlCenter(id string) *GroundControlCenter {
	return &GroundControlCenter{
		ID:           id,
		inboundQueue: make(chan ACARSMessageInterface, 50), // 为其分配一个带缓冲的队列
	}
}

// StartListening 启动地面站的监听服务。
func (gcc *GroundControlCenter) StartListening(comms *CommunicationSystem) {
	comms.RegisterListener(gcc.inboundQueue) // 通过管理器注册
	log.Printf("🛰️  地面站 [%s] 已启动，开始监听主/备信道...", gcc.ID)

	for msg := range gcc.inboundQueue {
		go gcc.processMessage(msg, comms) // 传递 comms
	}
}

// processMessage 是内部处理方法，处理单个报文并发送 ACK。
func (gcc *GroundControlCenter) processMessage(msg ACARSMessageInterface, comms *CommunicationSystem) {
	baseMsg := msg.GetBaseMessage()

	// 如果是自己发出的消息，应当不进行任何操作。
	if baseMsg.AircraftICAOAddress == gcc.ID {
		return
	}

	time.Sleep(ProcessingDelay)

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

	// 使用我们为 ACK 创建的专用高优先级构造函数
	ackMessage, err := NewCriticalPriorityMessage(ackBaseMsg, ackData)
	if err != nil {
		log.Printf("错误: [%s] 创建 ACK 报文失败: %v", gcc.ID, err)
		return
	}
	dynamicTimeSlot := comms.GetCurrentTimeSlot()
	// 调用 SendMessage 将 ACK 发送回信道
	go gcc.SendMessage(ackMessage, comms, dynamicTimeSlot)
}

func (gcc *GroundControlCenter) SendMessage(msg ACARSMessageInterface, comms *CommunicationSystem, timeSlot time.Duration) {
	baseMsg := msg.GetBaseMessage()
	sendStartTime := time.Now()
	log.Printf("🚀 [%s] 准备发送 ACK (ID: %s)", gcc.ID, baseMsg.MessageID)

	// --- 核心修改: 动态选择信道 ---
	// ACK 报文是高优先级，适用双信道逻辑
	targetChannel := comms.SelectChannelForMessage(msg, gcc.ID)
	p := targetChannel.GetPForMessage(msg.GetPriority())

	for {
		atomic.AddUint64(&gcc.totalRqTunnel, 1)
		if !targetChannel.IsBusy() {
			if rand.Float64() < p {
				if targetChannel.AttemptTransmit(msg, gcc.ID, timeSlot) {
					waitTime := time.Since(sendStartTime)
					gcc.totalWaitTimeNs.Add(waitTime.Nanoseconds())
					atomic.AddUint64(&gcc.totalTxAttempts, 1)
					atomic.AddUint64(&gcc.successfulTx, 1)
					return
				} else {
					atomic.AddUint64(&gcc.totalTxAttempts, 1)
					atomic.AddUint64(&gcc.totalCollisions, 1)
					log.Printf("💥 [%s] 在信道上碰撞！发送 ACK 失败，避退后重试", gcc.ID)
				}
			} else {
				log.Printf("🤔 [%s] 信道空闲，但决定延迟发送 ACK (p=%.2f)...", gcc.ID, p)
			}
		} else {
			atomic.AddUint64(&gcc.totalFailRqTunnel, 1)
			log.Printf("⏳ [%s] 信道忙，等待发送 ACK...", gcc.ID)
		}
		time.Sleep(timeSlot)
	}
}

func (gcc *GroundControlCenter) GetCommunicationStats() string {
	// 使用 atomic.LoadUint64 来安全地读取计数值
	attempts := atomic.LoadUint64(&gcc.totalTxAttempts)
	collisions := atomic.LoadUint64(&gcc.totalCollisions)
	successes := atomic.LoadUint64(&gcc.successfulTx)

	var collisionRate float64
	if attempts > 0 {
		collisionRate = (float64(collisions) / float64(attempts)) * 100
	}

	stats := fmt.Sprintf("--- 通信统计 地面站 %s ---\n", gcc.ID)
	stats += fmt.Sprintf("  - 成功发送报文数: %d\n", successes)
	stats += fmt.Sprintf("  - 总传输尝试次数: %d\n", attempts)
	stats += fmt.Sprintf("  - 碰撞/信道访问失败次数: %d\n", collisions)
	stats += fmt.Sprintf("  - 碰撞率 (失败/尝试): %.2f%%\n", collisionRate)
	stats += "--------------------------------------\n"

	return stats
}

func (gcc *GroundControlCenter) ResetStats() {
	atomic.StoreUint64(&gcc.totalTxAttempts, 0)
	atomic.StoreUint64(&gcc.totalCollisions, 0)
	atomic.StoreUint64(&gcc.successfulTx, 0)
	atomic.StoreUint64(&gcc.totalRqTunnel, 0)
	atomic.StoreUint64(&gcc.totalFailRqTunnel, 0)
	gcc.totalWaitTimeNs.Store(0)
}

// GroundControlRawStats Excel自动统计需要以下两个函数
type GroundControlRawStats struct {
	SuccessfulTx      uint64
	TotalTxAttempts   uint64
	TotalCollisions   uint64
	TotalRqTunnel     uint64
	TotalFailRqTunnel uint64
	TotalWaitTimeNs   time.Duration
}

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
