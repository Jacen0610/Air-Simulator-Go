// C:/workspace/go/Air-Simulator-Go/center.go
package main

import (
	"fmt"
	"log"
	"time"
)

// GroundControlCenter 代表一个地面控制站，负责处理接收到的 ACARS 报文。
type GroundControlCenter struct {
	ID string // 地面站的唯一标识符, 例如 "ZSSS_GND"
}

// NewGroundControlCenter 是 GroundControlCenter 的构造函数。
func NewGroundControlCenter(id string) *GroundControlCenter {
	return &GroundControlCenter{
		ID: id,
	}
}

// getProcessingDelay 确定处理报文所需的延迟时间。
func (gcc *GroundControlCenter) getProcessingDelay() time.Duration {
	return 250 * time.Millisecond
}

// ProcessMessage 模拟接收并处理一个 ACARS 报文。
// 这个方法现在由发送方（例如飞机）在成功通过信道传输后直接调用。
// 它本身是阻塞的，以模拟处理时间，调用方应该在一个新的 goroutine 中调用它，
// 以避免发送方被阻塞。
func (gcc *GroundControlCenter) ProcessMessage(msg ACARSMessageInterface, commsChannel *Channel) {
	baseMsg := msg.GetBaseMessage()

	log.Printf("🛰️  [%s] 接收到报文: ID=%s, 来自: %s (航班: %s)\n",
		gcc.ID, baseMsg.MessageID, baseMsg.AircraftICAOAddress, baseMsg.FlightID)

	delay := gcc.getProcessingDelay()
	log.Printf("⚙️  [%s] 正在处理报文 %s... (模拟延迟: %v)\n", gcc.ID, baseMsg.MessageID, delay)
	time.Sleep(delay)

	log.Printf("✅ [%s] 报文 %s 处理完毕，准备发送 ACK...", gcc.ID, baseMsg.MessageID)

	// 创建 ACK 报文
	ackData := AcknowledgementData{
		OriginalMessageID: baseMsg.MessageID,
		Status:            "RECEIVED",
	}
	ackBaseMsg := ACARSBaseMessage{
		AircraftICAOAddress: gcc.ID, // 发送方是地面站
		FlightID:            "GND_CTL",
		MessageID:           fmt.Sprintf("ACK-%s", baseMsg.MessageID),
		Timestamp:           time.Now(),
		Type:                MsgTypeAck,
	}
	ackMessage, err := NewCriticalHighPriorityMessage(ackBaseMsg, ackData)
	if err != nil {
		log.Printf("错误: [%s] 创建 ACK 报文失败: %v", gcc.ID, err)
		return
	}

	// 在一个新的 goroutine 中发送 ACK，这样 GCS 就不会被发送过程阻塞，可以继续处理其他消息
	go gcc.SendMessage(ackMessage, commsChannel)
}

// 新增: SendMessage 方法，使地面站也能作为发送方争用信道。
// 这个方法的逻辑与飞机上的 SendMessage 完全相同。
func (gcc *GroundControlCenter) SendMessage(msg ACARSMessageInterface, commsChannel *Channel) {
	baseMsg := msg.GetBaseMessage()
	transmissionTime := 150 * time.Millisecond // ACK 报文通常较短，传输时间也短一些

	for {
		if !commsChannel.IsBusy() {
			commsChannel.SetBusy(true)
			log.Printf("🛰️  [%s] 获得信道，开始传输 ACK 报文 (ID: %s)", gcc.ID, baseMsg.MessageID)

			time.Sleep(transmissionTime)

			// 在模拟中，我们只记录 ACK 已发送，不需要对方再确认
			log.Printf("📡 [%s] ACK 报文 (ID: %s) 已发送。", gcc.ID, baseMsg.MessageID)

			commsChannel.SetBusy(false)
			log.Printf("📡 [%s] 传输完成，释放信道。", gcc.ID)
			return
		}

		log.Printf("⏳ [%s] 信道忙，等待发送 ACK (ID: %s)...", gcc.ID, baseMsg.MessageID)
		time.Sleep(200 * time.Millisecond)
	}
}
