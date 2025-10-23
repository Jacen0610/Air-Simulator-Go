package simulation

import (
	"Air-Simulator/config"
	"fmt"
	"log"
	"time"
)

// [大幅简化] GroundControlCenter 代表一个地面控制站。
// 它不再作为竞争节点，而是作为响应节点，使用优先信道发送ACK。
type GroundControlCenter struct {
	ID           string
	inboundQueue chan ACARSMessageInterface // 自己的内部消息队列
}

// NewGroundControlCenter 是 GroundControlCenter 的构造函数。
func NewGroundControlCenter(id string) *GroundControlCenter {
	return &GroundControlCenter{
		ID:           id,
		inboundQueue: make(chan ACARSMessageInterface, 100), // 为其分配一个带缓冲的队列
	}
}

// StartListening 启动地面站的监听服务。
func (gcc *GroundControlCenter) StartListening(commsSystem *CommunicationSystem) {
	commsSystem.RegisterListener(gcc.inboundQueue)
	log.Printf("🛰️  地面站 [%s] 已启动，开始监听通信系统...", gcc.ID)

	// 不再需要 ProcessOutbox 协程

	for msg := range gcc.inboundQueue {
		// 直接在 goroutine 中处理消息，以避免阻塞监听循环
		go gcc.processMessage(msg, commsSystem)
	}
}

// [核心修改] processMessage 处理收到的报文，并使用 ForceTransmit 发送 ACK。
func (gcc *GroundControlCenter) processMessage(msg ACARSMessageInterface, commsSystem *CommunicationSystem) {
	baseMsg := msg.GetBaseMessage()

	// 过滤掉来自背景流量生成器的报文
	if baseMsg.AircraftICAOAddress == BackgroundTrafficICAO {
		return
	}

	// 过滤掉自己发送的消息（虽然现在不太可能发生）
	if baseMsg.AircraftICAOAddress == gcc.ID {
		return
	}

	// 模拟处理延迟
	time.Sleep(config.ProcessingDelay)

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

	// ACK 通常是最高优先级的控制信令
	ackMessage, err := NewCriticalPriorityMessage(ackBaseMsg, ackData)
	if err != nil {
		log.Printf("错误: [%s] 创建 ACK 报文失败: %v", gcc.ID, err)
		return
	}

	// [核心修改] 直接调用 ForceTransmit，绕过 CSMA 竞争
	commsSystem.PrimaryChannel.ForceTransmit(ackMessage, gcc.ID)
}

// ResetStats - 地面站不再有需要重置的竞争统计数据。
// 这个函数可以保留为空，以满足可能的接口要求，或者直接移除。
func (gcc *GroundControlCenter) ResetStats() {
	// 无操作
}

// GroundControlRawStats - 定义一个空的结构体或移除，因为不再有统计数据。
type GroundControlRawStats struct {
	// 无统计数据
}

// GetRawStats - 返回空结构体。
func (gcc *GroundControlCenter) GetRawStats() GroundControlRawStats {
	return GroundControlRawStats{}
}
