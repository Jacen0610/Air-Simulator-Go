// C:/workspace/go/Air-Simulator-Go/center.go
package main

import (
	"fmt"
	"log"
	"math/rand"
	"time"
)

// GroundControlCenter 代表一个地面控制站。
type GroundControlCenter struct {
	ID           string
	inboundQueue chan ACARSMessageInterface // 自己的内部消息队列
}

// NewGroundControlCenter 是 GroundControlCenter 的构造函数。
func NewGroundControlCenter(id string) *GroundControlCenter {
	return &GroundControlCenter{
		ID:           id,
		inboundQueue: make(chan ACARSMessageInterface, 50), // 为其分配一个带缓冲的队列
	}
}

// StartListening 启动地面站的监听服务。
// 它会向一个通信信道注册自己，并持续处理收到的消息。
// 这个方法应该在一个单独的 goroutine 中运行。
func (gcc *GroundControlCenter) StartListening(commsChannel *Channel, pMap PriorityPMap, timeSlot time.Duration) {
	// 向主信道注册自己的接收队列
	commsChannel.RegisterListener(gcc.inboundQueue)
	log.Printf("🛰️  地面站 [%s] 已启动，开始监听信道...", gcc.ID)

	// 开启一个循环，专门处理自己队列中的消息
	for msg := range gcc.inboundQueue {
		// 为每个消息启动一个 goroutine 进行处理，以实现并发
		go gcc.processMessage(msg, commsChannel, pMap, timeSlot)
	}
}

// getProcessingDelay 模拟处理报文所需的时间。
func (gcc *GroundControlCenter) getProcessingDelay() time.Duration {
	return 100 * time.Millisecond
}

// processMessage 是内部处理方法，处理单个报文并发送 ACK。
func (gcc *GroundControlCenter) processMessage(msg ACARSMessageInterface, commsChannel *Channel, pMap PriorityPMap, timeSlot time.Duration) {
	baseMsg := msg.GetBaseMessage()

	// 如果是自己发出的消息，应当不进行任何操作。
	if baseMsg.AircraftICAOAddress == gcc.ID {
		log.Printf("ℹ️  [%s] 收到 ACK 报文 (ID: %s)，无需处理。", gcc.ID, baseMsg.MessageID)
		return
	}

	log.Printf("🛰️  [%s] 从队列中取出报文进行处理: ID=%s, 来自: %s\n", gcc.ID, baseMsg.MessageID, baseMsg.FlightID)

	delay := gcc.getProcessingDelay()
	log.Printf("⚙️  [%s] 正在处理报文 %s... (模拟延迟: %v)\n", gcc.ID, baseMsg.MessageID, delay)
	time.Sleep(delay)

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
	ackMessage, err := NewCriticalHighPriorityMessage(ackBaseMsg, ackData)
	if err != nil {
		log.Printf("错误: [%s] 创建 ACK 报文失败: %v", gcc.ID, err)
		return
	}

	// 调用 SendMessage 将 ACK 发送回信道
	go gcc.SendMessage(ackMessage, commsChannel, pMap, timeSlot)
}

func (gcc *GroundControlCenter) SendMessage(msg ACARSMessageInterface, commsChannel *Channel, pMap PriorityPMap, timeSlot time.Duration) {
	baseMsg := msg.GetBaseMessage()
	p := pMap[msg.GetPriority()]
	transmissionTime := 80 * time.Millisecond // ACK 报文传输时间较短

	// 地面站发送 ACK 时也需要争用信道
	log.Printf("🚀 [%s] 准备发送 ACK (ID: %s)", gcc.ID, baseMsg.MessageID)
	for {
		if !commsChannel.IsBusy() {
			if rand.Float64() < p {
				if commsChannel.AttemptTransmit(msg, gcc.ID, transmissionTime) {
					// ACK 发送成功，地面站的任务完成，它不需要等待对 ACK 的 ACK
					return
				}
			} else {
				log.Printf("🤔 [%s] 信道空闲，但决定延迟发送 ACK (p=%.2f)...", gcc.ID, p)
			}
		} else {
			log.Printf("⏳ [%s] 信道忙，等待发送 ACK...", gcc.ID)
		}
		time.Sleep(timeSlot)
	}
}
