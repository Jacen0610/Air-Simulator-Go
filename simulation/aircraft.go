package simulation

import (
	"Air-Simulator/config"
	"encoding/json"
	"log"
	"math"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// ackWaiter 结构体，存储等待ACK的报文信息
type ackWaiter struct {
	message     ACARSMessageInterface
	sendTime    time.Time
	enqueueTime time.Time // **[保留]** 报文原始入队时间，用于超时重传
}

// outboxItem 用于在发件箱中存储消息及其元数据
type outboxItem struct {
	message          ACARSMessageInterface
	enqueueTime      time.Time
	isRetransmission bool // [新增] 标记此条目是否为重传
}

// Aircraft 结构体定义了一架航空器的所有关键参数
type Aircraft struct {
	// ... (结构体其他字段保持不变)
	ICAOAddress  string `json:"icaoAddress"`
	Registration string `json:"registration"`
	AircraftType string `json:"aircraftType"`
	Manufacturer string `json:"manufacturer"`
	SerialNumber string `json:"serialNumber"`

	AirlineICAOCode    string          `json:"airlineICAOCode"`
	CurrentFlightID    string          `json:"currentFlightID"`
	CurrentFlightPhase string          `json:"currentFlightPhase"`
	LastOOOIReport     *OOOIReportData `json:"lastOOOIReport,omitempty"`

	CurrentPosition         *PositionReportData       `json:"currentPosition,omitempty"`
	FuelRemainingKG         float64                   `json:"fuelRemainingKG"`
	FuelConsumptionRateKGPH float64                   `json:"fuelConsumptionRateKGPH"`
	EngineStatus            map[int]*EngineReportData `json:"engineStatus,omitempty"`
	LastDataReportTimestamp time.Time                 `json:"lastDataReportTimestamp"`
	SquawkCode              string                    `json:"squawkCode"`

	ACARSEnabled          bool   `json:"acarsEnabled"`
	CPDLCEnabled          bool   `json:"cpdlcEnabled"`
	SatelliteCommsEnabled bool   `json:"satelliteCommsEnabled"`
	SoftwareVersion       string `json:"softwareVersion"`

	inboundQueue  chan ACARSMessageInterface
	outboundQueue []outboxItem // 发件箱
	outboundMutex sync.RWMutex
	ackWaiters    sync.Map

	totalTxAttempts   uint64
	totalCollisions   uint64
	successfulTx      uint64
	totalRetries      uint64
	totalRqTunnel     uint64
	totalFailRqTunnel uint64
	totalWaitTimeNs   atomic.Int64
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
		message:          msg,
		enqueueTime:      time.Now(),
		isRetransmission: false, // 新消息不是重传
	}
	a.outboundQueue = append(a.outboundQueue, item)

	sort.Slice(a.outboundQueue, func(i, j int) bool {
		return a.outboundQueue[i].message.GetBaseMessage().Timestamp.Before(a.outboundQueue[j].message.GetBaseMessage().Timestamp)
	})
	log.Printf("📥 [飞机 %s] 新消息 (ID: %s) 已进入发送队列。当前队列长度: %d", a.CurrentFlightID, msg.GetBaseMessage().MessageID, len(a.outboundQueue))
}

// peekMessage 查看（不移除）队列头部的消息条目。
func (a *Aircraft) peekMessage() *outboxItem {
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

// StartListening 只负责处理ACK确认。
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

	if topItem := a.peekMessage(); topItem != nil {
		obs.HasMessage = true
		obs.TopMessageWaitTimeSeconds = float32(time.Since(topItem.enqueueTime).Seconds())
		obs.IsRetransmission = topItem.isRetransmission
	} else {
		obs.HasMessage = false
		obs.TopMessageWaitTimeSeconds = 0
		obs.IsRetransmission = false
	}

	return obs
}

// Step 函数: 执行一步决策并返回奖励
func (a *Aircraft) Step(action AgentAction, comms *CommunicationSystem) float32 {
	reward := float32(0)

	// 1. 处理 ACK 超时和重传
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

			reward -= 25.0

			item := outboxItem{
				message:          waiter.message,
				enqueueTime:      time.Now(),
				isRetransmission: true,
			}
			a.outboundQueue = append(a.outboundQueue, item)
		}
		sort.Slice(a.outboundQueue, func(i, j int) bool {
			return a.outboundQueue[i].message.GetBaseMessage().Timestamp.Before(a.outboundQueue[j].message.GetBaseMessage().Timestamp)
		})
		a.outboundMutex.Unlock()
	}

	// 2. 根据动作执行决策
	itemToSend := a.peekMessage()
	if itemToSend == nil {
		if action == ActionSendPrimary || action == ActionSendBackup {
			reward -= 10.0
		} else {
			reward += 1.0
		}
		return reward
	}

	switch action {
	case ActionWait:
		if comms.PrimaryChannel.IsBusy() && (comms.BackupChannel == nil || comms.BackupChannel.IsBusy()) {
			reward += 0.5
		} else {
			waitTime := float32(time.Since(itemToSend.enqueueTime).Seconds())
			penalty := 1.0 + waitTime*2.0
			if itemToSend.isRetransmission {
				penalty += 15.0
			}
			reward -= penalty
		}
	case ActionSendPrimary:
		reward += a.attemptSendOnChannel(itemToSend, comms.PrimaryChannel)

	case ActionSendBackup:
		if comms.BackupChannel != nil {
			reward += a.attemptSendOnChannel(itemToSend, comms.BackupChannel)
		} else {
			reward -= 15.0
		}
	}
	return reward
}

// attemptSendOnChannel 尝试在指定信道上发送消息
func (a *Aircraft) attemptSendOnChannel(item *outboxItem, channel *Channel) float32 {
	atomic.AddUint64(&a.totalRqTunnel, 1)
	time.Sleep(time.Duration(10+rand.Intn(41)) * time.Microsecond)

	if channel.IsBusy() {
		atomic.AddUint64(&a.totalFailRqTunnel, 1)
		return -2.0
	}

	atomic.AddUint64(&a.totalTxAttempts, 1)
	msg := item.message

	if channel.AttemptTransmit(msg, a.CurrentFlightID, config.TransmissionTime) {
		waitTime := time.Since(item.enqueueTime)
		a.totalWaitTimeNs.Add(waitTime.Nanoseconds())
		log.Printf("✈️  [飞机 %s] 成功抢占信道并发送报文 (ID: %s)。排队等待时间: %s", a.CurrentFlightID, msg.GetBaseMessage().MessageID, waitTime)

		a.removeMessageFromQueue(msg.GetBaseMessage().MessageID)
		waiter := &ackWaiter{
			message:     msg,
			sendTime:    time.Now(),
			enqueueTime: item.enqueueTime,
		}
		a.ackWaiters.Store(msg.GetBaseMessage().MessageID, waiter)

		// [核心修改] 使用指数衰减函数计算奖励
		const maxReward = 20.0
		const benchmarkTime = 0.4 // 400ms
		const decayConstant = 5.0

		waitTimeSeconds := waitTime.Seconds()
		var reward float32
		if waitTimeSeconds > benchmarkTime {
			excessTime := waitTimeSeconds - benchmarkTime
			reward = float32(maxReward * math.Exp(-decayConstant*excessTime))
		} else {
			// 如果实际等待时间小于等于理论极限，给予最大奖励
			// 这种情况理论上不应发生，但作为代码鲁棒性保证
			reward = maxReward
		}
		return reward
	} else {
		atomic.AddUint64(&a.totalCollisions, 1)
		log.Printf("💥 [飞机 %s] 发送报文 (ID: %s) 时失败(碰撞)。", a.CurrentFlightID, msg.GetBaseMessage().MessageID)
		return -50.0
	}
}

// Reset 重置飞机状态
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

// AircraftRawStats 定义了用于数据收集的原始统计数据结构
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

// GetRawStats 返回原始统计数据
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
