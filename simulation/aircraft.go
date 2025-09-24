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

// ackWaiter 结构体，存储等待ACK的报文信息
type ackWaiter struct {
	message     ACARSMessageInterface
	sendTime    time.Time
	enqueueTime time.Time // **[保留]** 报文原始入队时间，用于超时重传
}

// outboxItem 用于在发件箱中存储消息及其入队时间
type outboxItem struct {
	message     ACARSMessageInterface
	enqueueTime time.Time
}

// Aircraft 结构体定义了一架航空器的所有关键参数
type Aircraft struct {
	// --- 识别与注册信息 ---
	ICAOAddress  string `json:"icaoAddress"`
	Registration string `json:"registration"`
	AircraftType string `json:"aircraftType"`
	Manufacturer string `json:"manufacturer"`
	SerialNumber string `json:"serialNumber"`

	// --- 运营与归属信息 ---
	AirlineICAOCode    string          `json:"airlineICAOCode"`
	CurrentFlightID    string          `json:"currentFlightID"`
	CurrentFlightPhase string          `json:"currentFlightPhase"`
	LastOOOIReport     *OOOIReportData `json:"lastOOOIReport,omitempty"`

	// --- 位置与状态信息 ---
	CurrentPosition         *PositionReportData       `json:"currentPosition,omitempty"`
	FuelRemainingKG         float64                   `json:"fuelRemainingKG"`
	FuelConsumptionRateKGPH float64                   `json:"fuelConsumptionRateKGPH"`
	EngineStatus            map[int]*EngineReportData `json:"engineStatus,omitempty"`
	LastDataReportTimestamp time.Time                 `json:"lastDataReportTimestamp"`
	SquawkCode              string                    `json:"squawkCode"`

	// --- 通信与系统能力 ---
	ACARSEnabled          bool   `json:"acarsEnabled"`
	CPDLCEnabled          bool   `json:"cpdlcEnabled"`
	SatelliteCommsEnabled bool   `json:"satelliteCommsEnabled"`
	SoftwareVersion       string `json:"softwareVersion"`

	// --- 通信与状态管理 ---
	inboundQueue  chan ACARSMessageInterface
	outboundQueue []outboxItem // 发件箱
	outboundMutex sync.RWMutex
	ackWaiters    sync.Map

	// --- 通信统计 ---
	totalTxAttempts   uint64       // 总传输尝试次数
	totalCollisions   uint64       // 碰撞/信道访问失败次数
	successfulTx      uint64       // 成功发送并收到ACK的报文总数
	totalRetries      uint64       // 总重传次数
	totalRqTunnel     uint64       // 总尝试请求隧道次数
	totalFailRqTunnel uint64       // 总失败请求隧道次数
	totalWaitTimeNs   atomic.Int64 // **[修改]** 总排队等待时间 (纳秒)
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
		EngineStatus:            make(map[int]*EngineReportData),
		LastDataReportTimestamp: time.Now(),
		inboundQueue:            make(chan ACARSMessageInterface, 20),
		outboundQueue:           make([]outboxItem, 0, 10),
		ackWaiters:              sync.Map{},
	}
}

// EnqueueMessage 将一个新消息放入飞机的发件箱。
func (a *Aircraft) EnqueueMessage(msg ACARSMessageInterface) {
	a.outboundMutex.Lock()
	defer a.outboundMutex.Unlock()

	item := outboxItem{
		message:     msg,
		enqueueTime: time.Now(),
	}
	a.outboundQueue = append(a.outboundQueue, item)

	sort.Slice(a.outboundQueue, func(i, j int) bool {
		return a.outboundQueue[i].message.GetBaseMessage().Timestamp.Before(a.outboundQueue[j].message.GetBaseMessage().Timestamp)
	})
	log.Printf("📥 [飞机 %s] 新消息 (ID: %s, Prio: %s) 已进入发送队列。", a.CurrentFlightID, msg.GetBaseMessage().MessageID, msg.GetPriority())
}

// peekHighestPriorityMessage 查看（不移除）最重要的消息条目。
func (a *Aircraft) peekHighestPriorityMessage() *outboxItem {
	a.outboundMutex.RLock()
	defer a.outboundMutex.RUnlock()
	if len(a.outboundQueue) == 0 {
		return nil
	}
	return &a.outboundQueue[0]
}

// removeMessageFromQueue 在消息成功启动传输后将其从队列中移除。
func (a *Aircraft) removeMessageFromQueue(messageID string) {
	a.outboundMutex.Lock()
	defer a.outboundMutex.Unlock()
	for i, item := range a.outboundQueue {
		if item.message.GetBaseMessage().MessageID == messageID {
			a.outboundQueue = append(a.outboundQueue[:i], a.outboundQueue[i+1:]...)
			return
		}
	}
}

// **[核心修改]** StartListening 现在只负责处理ACK确认，不再计算等待时间。
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

		if _, ok := a.ackWaiters.LoadAndDelete(ackData.OriginalMessageID); ok {
			// **[修改]** 移除等待时间计算，只记录成功收到ACK的事件。
			log.Printf("🎉 [飞机 %s] 成功收到对报文 %s 的 ACK!", a.CurrentFlightID, ackData.OriginalMessageID)
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

	var timedOutWaiters []*ackWaiter
	a.ackWaiters.Range(func(key, value interface{}) bool {
		waiter := value.(*ackWaiter)
		if time.Since(waiter.sendTime) > config.AckTimeout {
			timedOutWaiters = append(timedOutWaiters, waiter)
		}
		return true
	})

	if len(timedOutWaiters) > 0 {
		a.outboundMutex.Lock()
		for _, waiter := range timedOutWaiters {
			a.ackWaiters.Delete(waiter.message.GetBaseMessage().MessageID)
			log.Printf("⏰ [飞机 %s] 等待报文 (ID: %s) 的 ACK 超时！将重新排队...", a.CurrentFlightID, waiter.message.GetBaseMessage().MessageID)
			atomic.AddUint64(&a.totalRetries, 1)

			item := outboxItem{
				message:     waiter.message,
				enqueueTime: time.Now(),
			}
			a.outboundQueue = append(a.outboundQueue, item)
		}
		// **[核心修改]** 超时重传的报文同样按时间戳排序，确保 FIFO
		sort.Slice(a.outboundQueue, func(i, j int) bool {
			return a.outboundQueue[i].enqueueTime.Before(a.outboundQueue[j].enqueueTime)
		})
		a.outboundMutex.Unlock()
	}

	itemToSend := a.peekHighestPriorityMessage()
	if itemToSend == nil {
		if action == ActionSendPrimary || action == ActionSendBackup {
			reward -= 10.0
		} else {
			reward += 1.0
		}
		return reward
	}

	msgToSend := itemToSend.message
	switch action {
	case ActionWait:
		if comms.PrimaryChannel.IsBusy() && comms.BackupChannel != nil && comms.BackupChannel.IsBusy() {
			reward += 0.5
		} else {
			a.outboundMutex.RLock()
			queueLen := len(a.outboundQueue)
			a.outboundMutex.RUnlock()
			priorityValue := msgToSend.GetPriority().Value()
			penalty := 1.0 + (float32(queueLen) * 0.5) + (float32(priorityValue) * 0.1)
			reward -= penalty
		}
	case ActionSendPrimary:
		reward += a.attemptSendOnChannel(itemToSend, comms.PrimaryChannel)

	case ActionSendBackup:
		if comms.BackupChannel != nil {
			reward += a.attemptSendOnChannel(itemToSend, comms.BackupChannel)
		} else {
			reward -= 10.0
		}
	}
	return reward
}

// **[核心修改]** attemptSendOnChannel 现在计算排队等待时间。
func (a *Aircraft) attemptSendOnChannel(item *outboxItem, channel *Channel) float32 {
	atomic.AddUint64(&a.totalRqTunnel, 1)
	if channel.isBusy {
		atomic.AddUint64(&a.totalFailRqTunnel, 1)
		return -2.0
	}
	atomic.AddUint64(&a.totalTxAttempts, 1)
	msg := item.message

	// 调用同步阻塞的 AttemptTransmit 函数
	if channel.AttemptTransmit(msg, a.CurrentFlightID, config.TransmissionTime) {
		// **[核心修改]** 传输成功启动，立即计算并累加排队等待时间。
		waitTime := time.Since(item.enqueueTime)
		a.totalWaitTimeNs.Add(waitTime.Nanoseconds())
		log.Printf("✈️  [飞机 %s] 成功抢占信道并发送报文 (ID: %s)。排队等待时间: %s", a.CurrentFlightID, msg.GetBaseMessage().MessageID, waitTime)

		// 从待发队列移除，并加入等待ACK的队列
		a.removeMessageFromQueue(msg.GetBaseMessage().MessageID)
		waiter := &ackWaiter{
			message:     msg,
			sendTime:    time.Now(),
			enqueueTime: item.enqueueTime, // 传递原始入队时间，以备超时重传
		}
		a.ackWaiters.Store(msg.GetBaseMessage().MessageID, waiter)

		return 3.0 // 成功抢占信道的奖励
	} else {
		// 发生碰撞或信道被占用
		atomic.AddUint64(&a.totalCollisions, 1)
		log.Printf("💥 [飞机 %s] 发送报文 (ID: %s) 时失败(碰撞或信道忙)。", a.CurrentFlightID, msg.GetBaseMessage().MessageID)
		return -10.0 // 碰撞或失败的惩罚
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

	a.outboundMutex.Lock()
	a.outboundQueue = make([]outboxItem, 0, 10)
	a.outboundMutex.Unlock()

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
