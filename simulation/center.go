// C:/workspace/go/Air-Simulator-Go/simulation/center.go
package simulation

import (
	"Air-Simulator/config"
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
	outboundQueue []ACARSMessageInterface    // 发送ACK的队列
	outboundMutex sync.RWMutex               // 保护队列的锁
	pendingReward atomic.Int64

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
		ID:            id,
		inboundQueue:  make(chan ACARSMessageInterface, 50), // 为其分配一个带缓冲的队列
		outboundQueue: make([]ACARSMessageInterface, 0, 20), // 初始化发件箱
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
		go gcc.processMessage(msg)
	}
}

// processMessage 是内部处理方法，处理单个报文并发送 ACK。
func (gcc *GroundControlCenter) processMessage(msg ACARSMessageInterface) {
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

	// 使用我们为 ACK 创建的专用高优先级构造函数
	ackMessage, err := NewCriticalPriorityMessage(ackBaseMsg, ackData)
	if err != nil {
		log.Printf("错误: [%s] 创建 ACK 报文失败: %v", gcc.ID, err)
		return
	}

	// 将 ACK 放入发件箱，等待 MARL 代理决策
	gcc.EnqueueMessage(ackMessage)
}

func (gcc *GroundControlCenter) EnqueueMessage(msg ACARSMessageInterface) {
	gcc.outboundMutex.Lock()
	defer gcc.outboundMutex.Unlock()

	gcc.outboundQueue = append(gcc.outboundQueue, msg)

	// **[IMPLEMENTED]** 根据ACK对应的原始消息优先级进行排序
	// 确保需要优先确认的消息排在队列前面
	sort.Slice(gcc.outboundQueue, func(i, j int) bool {
		var prioI, prioJ int

		// 安全地提取并比较第一个消息的原始优先级
		if dataI, ok := gcc.outboundQueue[i].GetData().(AcknowledgementData); ok {
			prioI = dataI.OriginMessagePriority.Value()
		}

		// 安全地提取并比较第二个消息的原始优先级
		if dataJ, ok := gcc.outboundQueue[j].GetData().(AcknowledgementData); ok {
			prioJ = dataJ.OriginMessagePriority.Value()
		}

		// 优先级值越大，越靠前
		return prioI > prioJ
	})

	log.Printf("📥 [地面站 %s] 新 ACK (ID: %s) 已进入发送队列并完成排序。", gcc.ID, msg.GetBaseMessage().MessageID)
}

// peekHighestPriorityMessage 查看（不移除）最重要的消息。
func (gcc *GroundControlCenter) peekHighestPriorityMessage() ACARSMessageInterface {
	gcc.outboundMutex.RLock()
	defer gcc.outboundMutex.RUnlock()
	if len(gcc.outboundQueue) == 0 {
		return nil
	}
	return gcc.outboundQueue[0]
}

// removeMessageFromQueue 在消息成功启动传输后将其从队列中移除。
func (gcc *GroundControlCenter) removeMessageFromQueue(messageID string) {
	gcc.outboundMutex.Lock()
	defer gcc.outboundMutex.Unlock()
	for i, msg := range gcc.outboundQueue {
		if msg.GetBaseMessage().MessageID == messageID {
			gcc.outboundQueue = append(gcc.outboundQueue[:i], gcc.outboundQueue[i+1:]...)
			return
		}
	}
}

// GetObservation 为地面站 MARL 代理生成当前的观测数据。
func (gcc *GroundControlCenter) GetObservation(comms *CommunicationSystem) AgentObservation {
	gcc.outboundMutex.RLock()
	queueLen := len(gcc.outboundQueue) // 安全地获取队列长度
	gcc.outboundMutex.RUnlock()

	obs := AgentObservation{
		PrimaryChannelBusy:  comms.PrimaryChannel.IsBusy(),
		PendingAcksCount:    int32(0),        // 地面站不等待ACK
		OutboundQueueLength: int32(queueLen), // **[核心修改]**
	}
	if comms.BackupChannel != nil {
		obs.BackupChannelBusy = comms.BackupChannel.IsBusy()
	}

	if topMsg := gcc.peekHighestPriorityMessage(); topMsg != nil {
		obs.HasMessage = true
		obs.TopMessagePriority = topMsg.GetPriority()
	} else {
		obs.HasMessage = false
	}

	return obs
}

// Step 是地面站 MARL 模式下的核心执行函数。
func (gcc *GroundControlCenter) Step(action AgentAction, comms *CommunicationSystem) float32 {
	if action == ActionWait {
		log.Printf("⏳ [地面站 %s] 选择等待，不发送消息。", gcc.ID)
	} else if action == ActionSendPrimary {
		log.Printf("📤 [地面站 %s] 选择发送主通道消息。", gcc.ID)
	} else {
		log.Printf("📤 [地面站 %s] 选择发送备用通道消息。", gcc.ID)
	}
	// 地面站没有异步奖励，因为不接收ACK
	reward := float32(0.0)

	// **[核心修改]** 在决策前，首先检查并清理所有已过期的ACK
	// 这是一个非常重要的机制，用于惩罚因拖延而导致的发送失败
	gcc.outboundMutex.Lock() // 需要写锁，因为我们会修改队列
	i := 0
	for i < len(gcc.outboundQueue) {
		msg := gcc.outboundQueue[i]
		// 检查自ACK创建以来经过的时间是否已超过飞机侧的超时阈值
		if time.Since(msg.GetBaseMessage().Timestamp) > config.AckTimeout {
			log.Printf("🗑️ [地面站 %s] 丢弃过期ACK (ID: %s)，因其已在队列中停留过久。", gcc.ID, msg.GetBaseMessage().MessageID)
			// 对智能体施加重罚，因为它未能及时处理这条消息
			reward -= 20.0

			// 从队列中移除该消息
			gcc.outboundQueue = append(gcc.outboundQueue[:i], gcc.outboundQueue[i+1:]...)
			// 注意：因为移除了一个元素，所以我们不增加 i，继续检查当前位置的新元素
		} else {
			// 只有当消息未被移除时，才将索引向后移动
			i++
		}
	}
	gcc.outboundMutex.Unlock()

	// 生存成本，鼓励尽快发送
	reward -= 0.1

	// 从发件箱获取当前最紧急的消息
	msgToSend := gcc.peekHighestPriorityMessage()
	if msgToSend == nil {
		// 如果没有消息要发，任何发送动作都是无效的
		if action == ActionSendPrimary || action == ActionSendBackup {
			reward -= 10.0
		} else {
			// 没有消息要发时，"等待"是最高效的正确行为，给予奖励
			reward += 1.0
		}
		return reward
	}

	switch action {
	case ActionWait:
		// **[核心修改]** 引入与飞机类似的动态惩罚机制
		gcc.outboundMutex.RLock()
		queueLen := len(gcc.outboundQueue)
		gcc.outboundMutex.RUnlock()

		// ACK的优先级是基于原始消息的
		var originalPriorityValue int
		// 安全地类型断言并获取原始消息的优先级
		if ackData, ok := msgToSend.GetData().(AcknowledgementData); ok {
			originalPriorityValue = ackData.OriginMessagePriority.Value()
		} else {
			// 为未来可能出现的非ACK消息提供回退
			originalPriorityValue = msgToSend.GetPriority().Value()
		}

		// 惩罚与队列长度和消息重要性挂钩
		// 地面站作为中心枢纽，其清空队列的紧迫性更高，因此惩罚系数可以设置得更大
		penalty := 1.0 + (float32(queueLen) * 0.5) + (float32(originalPriorityValue) * 0.2)
		reward -= penalty

	case ActionSendPrimary:
		reward += gcc.attemptSendOnChannel(msgToSend, comms.PrimaryChannel)
	case ActionSendBackup:
		if comms.BackupChannel != nil {
			reward += gcc.attemptSendOnChannel(msgToSend, comms.BackupChannel)
		} else {
			reward -= 10.0 // 惩罚无效动作
		}
	}

	return reward
}

// attemptSendOnChannel 是 Step 函数的辅助方法，封装了在特定信道上尝试发送的逻辑
func (gcc *GroundControlCenter) attemptSendOnChannel(msg ACARSMessageInterface, channel *Channel) float32 {
	atomic.AddUint64(&gcc.totalRqTunnel, 1)
	if channel.IsBusy() {
		atomic.AddUint64(&gcc.totalFailRqTunnel, 1)
		return -1.0 // 信道忙，小幅惩罚
	}

	atomic.AddUint64(&gcc.totalTxAttempts, 1)
	if channel.AttemptTransmit(msg, gcc.ID, config.TransmissionTime) {
		// 成功启动传输，从队列中移除
		gcc.removeMessageFromQueue(msg.GetBaseMessage().MessageID)
		atomic.AddUint64(&gcc.successfulTx, 1) // 统计上，启动传输就算成功
		// 给予一个正奖励，因为成功抢占了信道并发送
		return 5.0 // 成功发送ACK的奖励
	} else {
		// 发生碰撞
		atomic.AddUint64(&gcc.totalCollisions, 1)
		return -10.0 // 碰撞，中度惩罚
	}
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

// Reset重置所有统计计数器。
func (gcc *GroundControlCenter) Reset() {
	atomic.StoreUint64(&gcc.totalTxAttempts, 0)
	atomic.StoreUint64(&gcc.totalCollisions, 0)
	atomic.StoreUint64(&gcc.successfulTx, 0)
	atomic.StoreUint64(&gcc.totalRqTunnel, 0)
	atomic.StoreUint64(&gcc.totalFailRqTunnel, 0)
	gcc.totalWaitTimeNs.Store(0)

	// 2. 清空消息队列
	gcc.outboundMutex.Lock()
	gcc.outboundQueue = make([]ACARSMessageInterface, 0, 20)
	gcc.outboundMutex.Unlock()
}

// GroundControlRawStats 定义了用于数据收集的原始统计数据结构。
type GroundControlRawStats struct {
	SuccessfulTx      uint64
	TotalTxAttempts   uint64
	TotalCollisions   uint64
	TotalRqTunnel     uint64
	TotalFailRqTunnel uint64
	TotalWaitTimeNs   time.Duration
	UnsentMessages    int
}

// GetRawStats 返回原始统计数据，用于写入报告。
func (gcc *GroundControlCenter) GetRawStats() GroundControlRawStats {
	gcc.outboundMutex.RLock()
	unsentMessage := len(gcc.outboundQueue)
	defer gcc.outboundMutex.RUnlock()
	return GroundControlRawStats{
		SuccessfulTx:      atomic.LoadUint64(&gcc.successfulTx),
		TotalTxAttempts:   atomic.LoadUint64(&gcc.totalTxAttempts),
		TotalCollisions:   atomic.LoadUint64(&gcc.totalCollisions),
		TotalRqTunnel:     atomic.LoadUint64(&gcc.totalRqTunnel),
		TotalFailRqTunnel: atomic.LoadUint64(&gcc.totalFailRqTunnel),
		TotalWaitTimeNs:   time.Duration(gcc.totalWaitTimeNs.Load()),
		UnsentMessages:    unsentMessage,
	}
}
