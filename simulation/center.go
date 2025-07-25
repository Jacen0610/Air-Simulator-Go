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
// 它现在向整个通信系统注册自己。
func (gcc *GroundControlCenter) StartListening(commsSystem *CommunicationSystem) {
	// 向通信系统注册自己的接收队列
	commsSystem.RegisterListener(gcc.inboundQueue)
	log.Printf("🛰️  地面站 [%s] 已启动，开始监听通信系统...", gcc.ID)

	// 开启一个循环，专门处理自己队列中的消息
	for msg := range gcc.inboundQueue {
		// 为每个消息启动一个 goroutine 进行处理，以实现并发
		go gcc.processMessage(msg, commsSystem)
	}
}

// processMessage 是内部处理方法，处理单个报文并发送 ACK。
func (gcc *GroundControlCenter) processMessage(msg ACARSMessageInterface, commsSystem *CommunicationSystem) {
	baseMsg := msg.GetBaseMessage()

	// 如果是自己发出的消息，应当不进行任何操作。
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

	// 使用我们为 ACK 创建的专用高优先级构造函数
	ackMessage, err := NewCriticalPriorityMessage(ackBaseMsg, ackData)
	if err != nil {
		log.Printf("错误: [%s] 创建 ACK 报文失败: %v", gcc.ID, err)
		return
	}

	// 调用 SendMessage 将 ACK 发送回通信系统
	go gcc.SendMessage(ackMessage, commsSystem)
}

// SendMessage 使用 p-坚持 CSMA 算法在选定的信道上发送报文。
// 它会持续尝试直到发送成功。
func (gcc *GroundControlCenter) SendMessage(msg ACARSMessageInterface, commsSystem *CommunicationSystem) {
	baseMsg := msg.GetBaseMessage()
	sendStartTime := time.Now()

	log.Printf("🚀 [%s] 准备发送 ACK (ID: %s, Prio: %s)", gcc.ID, baseMsg.MessageID, msg.GetPriority())

	// 地面站将持续尝试发送 ACK 直到成功
	for {
		// 1. 在每次循环时都动态选择最佳信道，以适应信道状态变化
		targetChannel := commsSystem.SelectChannelForMessage(msg, gcc.ID)
		p := targetChannel.GetPForMessage(msg.GetPriority())
		timeSlotForChannel := targetChannel.GetCurrentTimeSlot()

		atomic.AddUint64(&gcc.totalRqTunnel, 1)

		if !targetChannel.IsBusy() {
			if rand.Float64() < p {
				// 只有在概率允许时才真正尝试传输，这构成一次“传输尝试”
				atomic.AddUint64(&gcc.totalTxAttempts, 1)

				// 尝试传输。ACK的传输时间也使用全局常量
				if targetChannel.AttemptTransmit(msg, gcc.ID, config.TransmissionTime) {
					// 发送成功！
					waitTime := time.Since(sendStartTime)
					gcc.totalWaitTimeNs.Add(waitTime.Nanoseconds())
					atomic.AddUint64(&gcc.successfulTx, 1)
					log.Printf("✅ [%s] 在信道 [%s] 上成功发送 ACK (ID: %s)", gcc.ID, targetChannel.ID, baseMsg.MessageID)
					return // 成功发送后退出函数
				} else {
					// 发生碰撞
					atomic.AddUint64(&gcc.totalCollisions, 1)
					log.Printf("💥 [%s] 在信道 [%s] 上发送 ACK 时发生碰撞！", gcc.ID, targetChannel.ID)
				}
			} else {
				// p-坚持算法决定延迟
				log.Printf("🤔 [%s] 信道 [%s] 空闲，但决定延迟发送 ACK (p=%.2f)...", gcc.ID, targetChannel.ID, p)
			}
		} else {
			// 信道忙
			atomic.AddUint64(&gcc.totalFailRqTunnel, 1)
			log.Printf("⏳ [%s] 发现信道 [%s] 忙，等待发送 ACK...", gcc.ID, targetChannel.ID)
		}

		// 2. 等待从目标信道获取的专属时隙，然后重试
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
