package simulation

import (
	"Air-Simulator/config"
	"encoding/json"
	"log"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const MAX_PENDING_ACKS = 3

// **[修改]** ackWaiter 结构体，增加报文入队时间
type ackWaiter struct {
	message     ACARSMessageInterface
	sendTime    time.Time
	enqueueTime time.Time // **[新增]** 报文进入发件箱的时间
}

// **[新增]** outboxItem 用于在发件箱中存储消息及其入队时间
type outboxItem struct {
	message     ACARSMessageInterface
	enqueueTime time.Time
}

// Aircraft 结构体定义了一架航空器的所有关键参数
type Aircraft struct {
	// --- 识别与注册信息 ---
	ICAOAddress  string `json:"icaoAddress"`  // ICAO 24 位地址，全球唯一
	Registration string `json:"registration"` // 注册号 / 机号 (例如: B-6001)
	AircraftType string `json:"aircraftType"` // 飞机型号 (例如: B737-800)
	Manufacturer string `json:"manufacturer"` // 制造商 (例如: Boeing)
	SerialNumber string `json:"serialNumber"` // 制造商序列号

	// --- 运营与归属信息 ---
	AirlineICAOCode    string          `json:"airlineICAOCode"`          // 所属航空公司 ICAO 代码 (例如: CCA)
	CurrentFlightID    string          `json:"currentFlightID"`          // 当前执飞航班号 (例如: CCA1234)
	CurrentFlightPhase string          `json:"currentFlightPhase"`       // 当前飞行阶段
	LastOOOIReport     *OOOIReportData `json:"lastOOOIReport,omitempty"` // 最新的 OOOI 报告，使用指针表示可能为空

	// --- 位置与状态信息 ---
	CurrentPosition         *PositionReportData       `json:"currentPosition,omitempty"` // 当前位置，使用指针表示可能为空
	FuelRemainingKG         float64                   `json:"fuelRemainingKG"`           // 剩余燃油量 (公斤)
	FuelConsumptionRateKGPH float64                   `json:"fuelConsumptionRateKGPH"`   // 实时燃油消耗率 (公斤/小时)
	EngineStatus            map[int]*EngineReportData `json:"engineStatus,omitempty"`    // 各个发动机的最新状态，键为发动机编号
	LastDataReportTimestamp time.Time                 `json:"lastDataReportTimestamp"`   // 最新状态数据报告时间
	SquawkCode              string                    `json:"squawkCode"`                // 应答机代码 (Transponder Code)

	// --- 通信与系统能力 ---
	ACARSEnabled          bool   `json:"acarsEnabled"`          // 是否启用 ACARS 功能
	CPDLCEnabled          bool   `json:"cpdlcEnabled"`          // 是否启用 CPDLC 功能
	SatelliteCommsEnabled bool   `json:"satelliteCommsEnabled"` // 是否启用卫星通信
	SoftwareVersion       string `json:"softwareVersion"`

	// --- 通信与状态管理 ---
	inboundQueue  chan ACARSMessageInterface // 自己的消息收件箱
	outboundQueue []outboxItem               // **[修改]** 飞机的"发件箱"，现在存储 outboxItem
	outboundMutex sync.RWMutex               // 新增: 用于保护发件箱的读写锁

	ackWaiters sync.Map

	// --- 通信统计 ---
	totalTxAttempts   uint64       // 总传输尝试次数
	totalCollisions   uint64       // 碰撞
	successfulTx      uint64       // 成功发送并收到ACK的报文总数
	totalRetries      uint64       // 总重传次数
	totalRqTunnel     uint64       // 总尝试请求隧道次数
	totalFailRqTunnel uint64       // 总失败请求隧道次数
	totalWaitTimeNs   atomic.Int64 // 总等待时间 (纳秒)
}

// NewAircraft 创建一个航空器实例的构造函数
func NewAircraft(icaoAddr, reg, aircraftType, manufacturer, serialNum, airlineCode string) *Aircraft {
	return &Aircraft{
		ICAOAddress:             icaoAddr,
		Registration:            reg,
		AircraftType:            aircraftType,
		Manufacturer:            manufacturer,
		SerialNumber:            serialNum,
		AirlineICAOCode:         airlineCode,
		EngineStatus:            make(map[int]*EngineReportData), // 初始化 Map
		LastDataReportTimestamp: time.Now(),
		inboundQueue:            make(chan ACARSMessageInterface, 20), // 初始化收件箱
		outboundQueue:           make([]outboxItem, 0, 10),
		ackWaiters:              sync.Map{}, // 初始时间
	}
}

// EnqueueMessage 将一个新消息放入飞机的发件箱。这是飞行计划的新接口。
func (a *Aircraft) EnqueueMessage(msg ACARSMessageInterface) {
	a.outboundMutex.Lock()
	defer a.outboundMutex.Unlock()
	// **[修改]** 将消息和当前时间包装成 outboxItem
	item := outboxItem{
		message:     msg,
		enqueueTime: time.Now(),
	}
	a.outboundQueue = append(a.outboundQueue, item)
	// 为了确保高优先级消息总是被先处理，我们在这里进行排序。
	// 注意：在高性能场景下，使用优先队列 (heap) 会更高效。
	sort.Slice(a.outboundQueue, func(i, j int) bool {
		// **[修改]** 比较 item 内部 message 的优先级
		return a.outboundQueue[i].message.GetPriority().Value() > a.outboundQueue[j].message.GetPriority().Value()
	})
	log.Printf("📥 [飞机 %s] 新消息 (ID: %s, Prio: %s) 已进入发送队列。", a.CurrentFlightID, msg.GetBaseMessage().MessageID, msg.GetPriority())
}

// **[修改]** 返回值改为 *outboxItem，以便同时获取消息和入队时间
func (a *Aircraft) peekHighestPriorityMessage() *outboxItem {
	a.outboundMutex.RLock()
	defer a.outboundMutex.RUnlock()
	if len(a.outboundQueue) == 0 {
		return nil
	}
	// 返回指向元素的指针，以减少复制开销
	return &a.outboundQueue[0]
}

// removeMessageFromQueue 是一个内部辅助函数，在消息成功发送后将其从队列中移除。
func (a *Aircraft) removeMessageFromQueue(messageID string) {
	a.outboundMutex.Lock()
	defer a.outboundMutex.Unlock()
	for i, item := range a.outboundQueue {
		// **[修改]** 从 item 中获取 message 进行比较
		if item.message.GetBaseMessage().MessageID == messageID {
			a.outboundQueue = append(a.outboundQueue[:i], a.outboundQueue[i+1:]...)
			return
		}
	}
}

func (a *Aircraft) StartListening(comms *CommunicationSystem) {
	comms.RegisterListener(a.inboundQueue)
	log.Printf("✈️  [飞机 %s] 的通信系统已启动，开始监听...", a.CurrentFlightID)

	for msg := range a.inboundQueue {
		if msg.GetBaseMessage().Type != MsgTypeAck {
			continue
		}
		var ackData AcknowledgementData
		if rawData, ok := msg.GetData().(json.RawMessage); ok {
			if err := json.Unmarshal(rawData, &ackData); err != nil {
				continue
			}
		} else {
			continue
		}

		// LoadAndDelete 是原子操作，非常适合这里
		// **[修改]** 现在 LoadAndDelete 会返回 ackWaiter
		if val, ok := a.ackWaiters.LoadAndDelete(ackData.OriginalMessageID); ok {
			waiter := val.(*ackWaiter)
			// **[新增]** 计算并累加等待时间
			waitTime := time.Since(waiter.enqueueTime)
			a.totalWaitTimeNs.Add(waitTime.Nanoseconds())

			// 只要成功删除了一个等待者，就说明我们收到了一个有效的ACK
			log.Printf("🎉 [飞机 %s] 成功收到对报文 %s 的 ACK! 等待时间: %s", a.CurrentFlightID, ackData.OriginalMessageID, waitTime)
			atomic.AddUint64(&a.successfulTx, 1)
		}
	}
}

// GetObservation 为 MARL 代理生成当前的观测数据
func (a *Aircraft) GetObservation(comms *CommunicationSystem) AgentObservation {
	a.outboundMutex.RLock()
	queueLen := len(a.outboundQueue)
	a.outboundMutex.RUnlock()

	var pendingAcks int32
	a.ackWaiters.Range(func(_, _ interface{}) bool {
		pendingAcks++
		return true
	})

	obs := AgentObservation{
		PrimaryChannelBusy:  comms.PrimaryChannel.IsBusy(),
		OutboundQueueLength: int32(queueLen),
		PendingAcksCount:    pendingAcks,
	}
	if comms.BackupChannel != nil {
		obs.BackupChannelBusy = comms.BackupChannel.IsBusy()
	}

	if topItem := a.peekHighestPriorityMessage(); topItem != nil {
		obs.HasMessage = true
		obs.TopMessagePriority = topItem.message.GetPriority()
	} else {
		obs.HasMessage = false
	}

	return obs
}

// Step 函数现在从发件箱取消息进行发送
func (a *Aircraft) Step(action AgentAction, comms *CommunicationSystem) float32 {
	reward := float32(0)

	// **[核心改造]** 1. 检查并处理所有在途消息的超时
	var timedOutWaiters []*ackWaiter // **[修改]** 使用统一的slice，避免map迭代顺序问题

	a.ackWaiters.Range(func(key, value interface{}) bool {
		waiter := value.(*ackWaiter)
		if time.Since(waiter.sendTime) > config.AckTimeout {
			timedOutWaiters = append(timedOutWaiters, waiter)
		}
		return true
	})

	// **[修改]** 对超时的消息进行处理，并保留原始入队时间
	if len(timedOutWaiters) > 0 {
		a.outboundMutex.Lock()
		for _, waiter := range timedOutWaiters {
			a.ackWaiters.Delete(waiter.message.GetBaseMessage().MessageID) // 从等待者中移除
			log.Printf("⏰ [飞机 %s] 等待报文 (ID: %s) 的 ACK 超时！将重新排队...", a.CurrentFlightID, waiter.message.GetBaseMessage().MessageID)
			atomic.AddUint64(&a.totalRetries, 1)

			// **[修复]** 重新入队时，保留原始的 enqueueTime，以确保等待时间统计的准确性
			item := outboxItem{
				message:     waiter.message,
				enqueueTime: waiter.enqueueTime,
			}
			a.outboundQueue = append(a.outboundQueue, item)
		}
		// 在所有超时消息都重新入队后，统一进行一次排序
		sort.Slice(a.outboundQueue, func(i, j int) bool {
			return a.outboundQueue[i].message.GetPriority().Value() > a.outboundQueue[j].message.GetPriority().Value()
		})
		a.outboundMutex.Unlock()
	}

	// 从发件箱获取当前最紧急的消息条目
	itemToSend := a.peekHighestPriorityMessage()
	var msgToSend ACARSMessageInterface
	if itemToSend != nil {
		msgToSend = itemToSend.message
	}

	// 根据有无消息和采取的行动来计算奖励
	if msgToSend == nil {
		// 如果没有消息要发，任何发送动作都是无效的
		if action == ActionSendPrimary || action == ActionSendBackup {
			reward -= 10.0 // 惩罚无效的发送动作
		} else {
			reward += 1.0
		}
	} else {
		switch action {
		case ActionWait:
			if comms.PrimaryChannel.IsBusy() && comms.BackupChannel != nil && comms.BackupChannel.IsBusy() {
				reward += 5.0
			} else {
				a.outboundMutex.RLock()
				queueLen := len(a.outboundQueue)
				a.outboundMutex.RUnlock()
				priorityValue := msgToSend.GetPriority().Value()
				penalty := 1.0 + (float32(queueLen) * 0.5) + (float32(priorityValue) * 0.1)
				reward -= penalty
			}
		case ActionSendPrimary:
			// **[修改]** 传递整个 item
			reward += a.attemptSendOnChannel(itemToSend, comms.PrimaryChannel)
		case ActionSendBackup:
			if comms.BackupChannel != nil {
				// **[修改]** 传递整个 item
				reward += a.attemptSendOnChannel(itemToSend, comms.BackupChannel)
			} else {
				reward -= 10.0
			}
		}
	}
	return reward
}

// **[修改]** 参数改为 *outboxItem
func (a *Aircraft) attemptSendOnChannel(item *outboxItem, channel *Channel) float32 {
	atomic.AddUint64(&a.totalRqTunnel, 1)
	if channel.IsBusy() {
		atomic.AddUint64(&a.totalFailRqTunnel, 1)
		return -3 // 信道忙，小幅惩罚
	}

	msg := item.message // 从 item 中获取消息

	atomic.AddUint64(&a.totalTxAttempts, 1)
	if channel.AttemptTransmit(msg, a.CurrentFlightID, config.TransmissionTime) {
		// **[核心修复]** 成功启动传输后，必须立即将其从待发队列中移除
		// 并将其注册到“在途等待ACK”的清单中。
		msgID := msg.GetBaseMessage().MessageID
		a.removeMessageFromQueue(msgID) // 从待办事项中移除

		// **[核心改造]** 将完整的消息、发送时间、入队时间存入等待者
		waiter := &ackWaiter{
			message:     msg,
			sendTime:    time.Now(),
			enqueueTime: item.enqueueTime, // **[新增]** 传递入队时间
		}
		a.ackWaiters.Store(msgID, waiter)

		// 给予一个小的正奖励，因为成功抢占了信道
		return 3
	} else {
		// 发生碰撞
		atomic.AddUint64(&a.totalCollisions, 1)
		return -10.0 // 碰撞，中度惩罚
	}
}

func (a *Aircraft) Reset() {
	atomic.StoreUint64(&a.totalTxAttempts, 0)
	atomic.StoreUint64(&a.totalCollisions, 0)
	atomic.StoreUint64(&a.successfulTx, 0)
	atomic.StoreUint64(&a.totalRetries, 0)
	atomic.StoreUint64(&a.totalRqTunnel, 0)
	atomic.StoreUint64(&a.totalFailRqTunnel, 0)
	a.totalWaitTimeNs.Store(0)

	// 清空消息队列和等待状态
	a.outboundMutex.Lock()
	a.outboundQueue = make([]outboxItem, 0, 10) // **[修改]** 使用新的 item 类型
	a.outboundMutex.Unlock()

	// **[核心改造]** 清空 ackWaiters
	a.ackWaiters.Range(func(key, value interface{}) bool {
		a.ackWaiters.Delete(key)
		return true
	})
}

// AircraftRawStats Excel自动统计需要以下两个函数
type AircraftRawStats struct {
	SuccessfulTx      uint64
	TotalTxAttempts   uint64
	TotalCollisions   uint64
	TotalRetries      uint64
	TotalRqTunnel     uint64
	TotalFailRqTunnel uint64
	TotalWaitTime     time.Duration
	UnsentMessages    int
}

func (a *Aircraft) GetRawStats() AircraftRawStats {
	a.outboundMutex.RLock()
	queueSize := len(a.outboundQueue)
	a.outboundMutex.RUnlock()
	return AircraftRawStats{
		SuccessfulTx:      atomic.LoadUint64(&a.successfulTx),
		TotalTxAttempts:   atomic.LoadUint64(&a.totalTxAttempts),
		TotalCollisions:   atomic.LoadUint64(&a.totalCollisions),
		TotalRetries:      atomic.LoadUint64(&a.totalRetries),
		TotalRqTunnel:     atomic.LoadUint64(&a.totalRqTunnel),
		TotalFailRqTunnel: atomic.LoadUint64(&a.totalFailRqTunnel),
		TotalWaitTime:     time.Duration(a.totalWaitTimeNs.Load()),
		UnsentMessages:    queueSize,
	}
}
