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

// **[新增]** ackWaiter 是一个内部结构体，用于在 ackWaiters 中存储等待确认的消息及其发送时间。
type ackWaiter struct {
	message  ACARSMessageInterface
	sendTime time.Time
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
	outboundQueue []ACARSMessageInterface    // 新增: 飞机的"发件箱"
	outboundMutex sync.RWMutex               // 新增: 用于保护发件箱的读写锁

	ackWaiters sync.Map

	// --- MARL 状态与奖励 ---
	pendingReward atomic.Int64 // 奖励银行，处理异步收到的ACK奖励

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
		outboundQueue:           make([]ACARSMessageInterface, 0, 10),
		ackWaiters:              sync.Map{}, // 初始时间
	}
}

// EnqueueMessage 将一个新消息放入飞机的发件箱。这是飞行计划的新接口。
func (a *Aircraft) EnqueueMessage(msg ACARSMessageInterface) {
	a.outboundMutex.Lock()
	defer a.outboundMutex.Unlock()
	a.outboundQueue = append(a.outboundQueue, msg)
	// 为了确保高优先级消息总是被先处理，我们在这里进行排序。
	// 注意：在高性能场景下，使用优先队列 (heap) 会更高效。
	sort.Slice(a.outboundQueue, func(i, j int) bool {
		return a.outboundQueue[i].GetPriority().Value() > a.outboundQueue[j].GetPriority().Value()
	})
	log.Printf("📥 [飞机 %s] 新消息 (ID: %s, Prio: %s) 已进入发送队列。", a.CurrentFlightID, msg.GetBaseMessage().MessageID, msg.GetPriority())
}

func (a *Aircraft) peekHighestPriorityMessage() ACARSMessageInterface {
	a.outboundMutex.RLock()
	defer a.outboundMutex.RUnlock()
	if len(a.outboundQueue) == 0 {
		return nil
	}
	return a.outboundQueue[0] // 因为我们保持了队列有序，第一个就是最重要的
}

// removeMessageFromQueue 是一个内部辅助函数，在消息成功发送后将其从队列中移除。
func (a *Aircraft) removeMessageFromQueue(messageID string) {
	a.outboundMutex.Lock()
	defer a.outboundMutex.Unlock()
	for i, msg := range a.outboundQueue {
		if msg.GetBaseMessage().MessageID == messageID {
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
		if _, ok := a.ackWaiters.LoadAndDelete(ackData.OriginalMessageID); ok {
			// 只要成功删除了一个等待者，就说明我们收到了一个有效的ACK
			log.Printf("🎉 [飞机 %s] 成功收到对报文 %s 的 ACK! (MARL)", a.CurrentFlightID, ackData.OriginalMessageID)
			a.pendingReward.Add(20) // 存入成功奖励
			atomic.AddUint64(&a.successfulTx, 1)
			// **[核心修复]** 消息在发送时已从outboundQueue移除，此处无需也无法再次移除。
			// a.removeMessageFromQueue(ackData.OriginalMessageID)
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

	if topMsg := a.peekHighestPriorityMessage(); topMsg != nil {
		obs.HasMessage = true
		obs.TopMessagePriority = topMsg.GetPriority()
	} else {
		obs.HasMessage = false
	}

	return obs
}

// Step 函数现在从发件箱取消息进行发送
func (a *Aircraft) Step(action AgentAction, comms *CommunicationSystem) float32 {
	reward := float32(a.pendingReward.Swap(0))

	// **[核心改造]** 1. 检查并处理所有在途消息的超时
	var messagesToRequeue []ACARSMessageInterface
	var idsToDelete []string

	a.ackWaiters.Range(func(key, value interface{}) bool {
		msgID := key.(string)
		waiter := value.(*ackWaiter)
		if time.Since(waiter.sendTime) > config.AckTimeout {
			messagesToRequeue = append(messagesToRequeue, waiter.message)
			idsToDelete = append(idsToDelete, msgID)
		}
		return true
	})

	// 对超时的消息进行处理
	for i, msgID := range idsToDelete {
		a.ackWaiters.Delete(msgID) // 从等待者中移除
		timedOutMsg := messagesToRequeue[i]
		log.Printf("⏰ [飞机 %s] 等待报文 (ID: %s) 的 ACK 超时！将重新排队...", a.CurrentFlightID, timedOutMsg.GetBaseMessage().MessageID)
		atomic.AddUint64(&a.totalRetries, 1)
		a.EnqueueMessage(timedOutMsg)
	}

	// 2. 获取当前等待的ACK数量
	var pendingAcks int
	a.ackWaiters.Range(func(_, _ interface{}) bool {
		pendingAcks++
		return true
	})

	// 从发件箱获取当前最紧急的消息
	msgToSend := a.peekHighestPriorityMessage()

	// 根据有无消息和采取的行动来计算奖励
	if msgToSend == nil {
		// 如果没有消息要发，任何发送动作都是无效的
		if action == ActionSendPrimary || action == ActionSendBackup {
			reward -= 10.0 // 惩罚无效的发送动作
		} else {
			reward += 1.0
		}
	} else {
		// 3. 检查发送窗口是否已满
		if pendingAcks >= MAX_PENDING_ACKS {
			// 如果窗口已满，任何发送动作都是无效的，但等待是合理的
			if action == ActionSendPrimary || action == ActionSendBackup {
				reward -= 10.0 // 重罚在窗口满时尝试发送的无效动作
			} else {
				reward += 1.0
			}
			// 等待是正确行为，不增不减
		} else {
			// 如果窗口未满，可以进行发送决策
			switch action {
			case ActionWait:
				a.outboundMutex.RLock()
				queueLen := len(a.outboundQueue)
				a.outboundMutex.RUnlock()
				priorityValue := msgToSend.GetPriority().Value()
				penalty := 1.0 + (float32(queueLen) * 0.2) + (float32(priorityValue) * 0.1)
				reward -= penalty
			case ActionSendPrimary:
				reward += a.attemptSendOnChannel(msgToSend, comms.PrimaryChannel)
			case ActionSendBackup:
				if comms.BackupChannel != nil {
					reward += a.attemptSendOnChannel(msgToSend, comms.BackupChannel)
				} else {
					reward -= 10.0
				}
			}
		}
	}
	return reward
}

func (a *Aircraft) attemptSendOnChannel(msg ACARSMessageInterface, channel *Channel) float32 {
	atomic.AddUint64(&a.totalRqTunnel, 1)
	if channel.IsBusy() {
		atomic.AddUint64(&a.totalFailRqTunnel, 1)
		return -1.0 // 信道忙，小幅惩罚
	}

	atomic.AddUint64(&a.totalTxAttempts, 1)
	if channel.AttemptTransmit(msg, a.CurrentFlightID, config.TransmissionTime) {
		// **[核心修复]** 成功启动传输后，必须立即将其从待发队列中移除
		// 并将其注册到“在途等待ACK”的清单中。
		msgID := msg.GetBaseMessage().MessageID
		a.removeMessageFromQueue(msgID) // 从待办事项中移除

		// **[核心改造]** 将完整的消息和发送时间存入等待者
		waiter := &ackWaiter{
			message:  msg,
			sendTime: time.Now(),
		}
		a.ackWaiters.Store(msgID, waiter)

		// 给予一个小的正奖励，因为成功抢占了信道
		return 3.0
	} else {
		// 发生碰撞
		atomic.AddUint64(&a.totalCollisions, 1)
		return -5.0 // 碰撞，中度惩罚
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
	a.outboundQueue = make([]ACARSMessageInterface, 0, 10)
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
