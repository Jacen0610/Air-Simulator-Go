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
	enqueueTime time.Time // 报文原始入队时间，用于超时重传
}

// outboxItem 用于在发件箱中存储消息及其元数据
type outboxItem struct {
	message          ACARSMessageInterface
	enqueueTime      time.Time
	isRetransmission bool // 标记此条目是否为重传
}

// AgentObservation 定义了强化学习代理的观测状态
type AgentObservation struct {
	IsChannelBusy             float32 `json:"is_channel_busy"`               // 1. 信道是否忙碌
	HasDataToSend             float32 `json:"has_data_to_send"`              // 2. 自身是否有数据待发送
	LastSendCausedCollision   float32 `json:"last_send_caused_collision"`    // 3. 上一次发送是否导致碰撞
	ChannelBusyRatio          float32 `json:"channel_busy_ratio"`            // 4. 信道拥堵率
	ConsecutiveIdleSteps      float32 `json:"consecutive_idle_steps"`        // 5. 连续空闲步数
	PacketWaitingTime         float32 `json:"packet_waiting_time"`           // 6. 数据包等待时间 (单位: 步)
	StepsSinceLastCollision   float32 `json:"steps_since_last_collision"`    // 7. 距离上次碰撞的步数
	OutboundQueueLength       float32 `json:"outbound_queue_length"`         // 8. [新增] 发件箱队列长度
	TopMessageWaitTimeSeconds float32 `json:"top_message_wait_time_seconds"` // 9. [新增] 队首消息的等待时间(秒)
}

// Aircraft 结构体定义了一架航空器的所有关键参数
type Aircraft struct {
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

	// 统计数据
	totalTxAttempts   uint64
	totalCollisions   uint64
	successfulTx      uint64
	totalRetries      uint64
	totalRqTunnel     uint64
	totalFailRqTunnel uint64
	totalWaitTimeNs   atomic.Int64
	waitTimes         []time.Duration
	waitTimesMutex    sync.Mutex

	// --- 强化学习状态追踪 ---
	lastSendCausedCollision bool   // 状态3
	channelBusyHistory      []bool // 状态4
	consecutiveIdleSteps    int    // 状态5
	stepsSinceLastCollision int    // 状态7
	packetWaitingSteps      int    // 状态6
	rlStateMutex            sync.RWMutex
}

// NewAircraft 创建一个航空器实例的构造函数
func NewAircraft(icaoAddr, reg, aircraftType, manufacturer, serialNum, airlineCode string) *Aircraft {
	a := &Aircraft{
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
		waitTimes:               make([]time.Duration, 0, 100),
	}
	a.Reset()
	return a
}

// EnqueueMessage 将一个新消息放入飞机的发件箱。
func (a *Aircraft) EnqueueMessage(msg ACARSMessageInterface) {
	a.outboundMutex.Lock()
	defer a.outboundMutex.Unlock()

	item := outboxItem{
		message:          msg,
		enqueueTime:      time.Now(),
		isRetransmission: false,
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

// GetObservation 为 MARL 代理生成当前的观测状态
func (a *Aircraft) GetObservation(comms *CommunicationSystem) AgentObservation {
	a.rlStateMutex.RLock()
	defer a.rlStateMutex.RUnlock()
	a.outboundMutex.RLock()
	defer a.outboundMutex.RUnlock()

	isBusy := comms.PrimaryChannel.IsBusy()
	isBusyVal := float32(0)
	if isBusy {
		isBusyVal = 1.0
	}

	queueLen := len(a.outboundQueue)
	hasDataVal := float32(0)
	if queueLen > 0 {
		hasDataVal = 1.0
	}

	lastSendCollisionVal := float32(0)
	if a.lastSendCausedCollision {
		lastSendCollisionVal = 1.0
	}

	var busyRatio float32
	if len(a.channelBusyHistory) > 0 {
		busyCount := 0
		for _, busy := range a.channelBusyHistory {
			if busy {
				busyCount++
			}
		}
		busyRatio = float32(busyCount) / float32(len(a.channelBusyHistory))
	}

	var topMsgWaitTime float32
	if topItem := a.peekMessage(); topItem != nil {
		topMsgWaitTime = float32(time.Since(topItem.enqueueTime).Seconds())
	}

	return AgentObservation{
		IsChannelBusy:             isBusyVal,
		HasDataToSend:             hasDataVal,
		LastSendCausedCollision:   lastSendCollisionVal,
		ChannelBusyRatio:          busyRatio,
		ConsecutiveIdleSteps:      float32(a.consecutiveIdleSteps),
		PacketWaitingTime:         float32(a.packetWaitingSteps),
		StepsSinceLastCollision:   float32(a.stepsSinceLastCollision),
		OutboundQueueLength:       float32(queueLen),
		TopMessageWaitTimeSeconds: topMsgWaitTime,
	}
}

// updateRLState 在每个 Step 开始时更新状态
func (a *Aircraft) updateRLState(comms *CommunicationSystem) {
	a.rlStateMutex.Lock()
	defer a.rlStateMutex.Unlock()

	const sequenceLength = 10
	isBusy := comms.PrimaryChannel.IsBusy()
	if len(a.channelBusyHistory) >= sequenceLength {
		a.channelBusyHistory = a.channelBusyHistory[1:]
	}
	a.channelBusyHistory = append(a.channelBusyHistory, isBusy)

	if !isBusy {
		a.consecutiveIdleSteps++
	} else {
		a.consecutiveIdleSteps = 0
	}

	a.stepsSinceLastCollision++

	if a.peekMessage() != nil {
		a.packetWaitingSteps++
	} else {
		a.packetWaitingSteps = 0
	}
}

// Step 函数: 执行一步决策并返回奖励
func (a *Aircraft) Step(action AgentAction, comms *CommunicationSystem) float32 {
	a.updateRLState(comms)

	reward := float32(0)
	itemToSend := a.peekMessage()

	if itemToSend == nil {
		a.rlStateMutex.Lock()
		a.lastSendCausedCollision = false
		a.rlStateMutex.Unlock()
		if action == ActionSend {
			reward -= 10.0
		} else {
			reward += 1.0
		}
		return reward
	}

	switch action {
	case ActionWait:
		if comms.PrimaryChannel.IsBusy() {
			reward -= 0.5
			break
		}
		a.rlStateMutex.Lock()
		a.lastSendCausedCollision = false
		a.rlStateMutex.Unlock()

		const stepPenaltyFactor = 0.01
		a.rlStateMutex.RLock()
		stepPenalty := stepPenaltyFactor * float32(a.packetWaitingSteps)
		a.rlStateMutex.RUnlock()
		reward -= stepPenalty

		const queuePenaltyFactor = 5.0
		a.rlStateMutex.RLock()
		QueuePenalty := queuePenaltyFactor * float32(len(a.outboundQueue))
		a.rlStateMutex.RUnlock()
		reward -= QueuePenalty

		const waitTimeFactor = 2.0
		waitTimePenalty := waitTimeFactor * float32(time.Since(itemToSend.enqueueTime).Seconds())
		reward -= waitTimePenalty

		const basePenalty = 20.0
		reward -= basePenalty

	case ActionSend:
		reward += a.attemptSendOnChannel(itemToSend, comms.PrimaryChannel)
	}
	return reward
}

// attemptSendOnChannel 尝试在指定信道上发送消息
func (a *Aircraft) attemptSendOnChannel(item *outboxItem, channel *Channel) float32 {
	atomic.AddUint64(&a.totalRqTunnel, 1)
	time.Sleep(time.Duration(10+rand.Intn(41)) * time.Microsecond)

	if channel.IsBusy() {
		atomic.AddUint64(&a.totalFailRqTunnel, 1)
		a.rlStateMutex.Lock()
		a.lastSendCausedCollision = false
		a.rlStateMutex.Unlock()
		return -0.5
	}

	atomic.AddUint64(&a.totalTxAttempts, 1)
	msg := item.message

	if channel.AttemptTransmit(msg, a.CurrentFlightID, config.TransmissionTime) {
		waitTime := time.Since(item.enqueueTime)
		a.totalWaitTimeNs.Add(waitTime.Nanoseconds())
		a.waitTimesMutex.Lock()
		a.waitTimes = append(a.waitTimes, waitTime)
		a.waitTimesMutex.Unlock()

		log.Printf("✈️  [飞机 %s] 成功抢占信道并发送报文 (ID: %s)。排队等待时间: %s", a.CurrentFlightID, msg.GetBaseMessage().MessageID, waitTime)

		a.removeMessageFromQueue(msg.GetBaseMessage().MessageID)
		waiter := &ackWaiter{
			message:     msg,
			sendTime:    time.Now(),
			enqueueTime: item.enqueueTime,
		}
		a.ackWaiters.Store(msg.GetBaseMessage().MessageID, waiter)

		a.rlStateMutex.Lock()
		a.lastSendCausedCollision = false
		a.packetWaitingSteps = 0
		a.rlStateMutex.Unlock()

		waitTimeSeconds := waitTime.Seconds()
		optimalTime := 0.4
		var reward float64

		if waitTimeSeconds <= optimalTime {
			reward = 50.0
		} else {
			timeDiff := waitTimeSeconds - optimalTime
			exponent := -1.4 * timeDiff * timeDiff
			reward = 49.0*math.Exp(exponent) + 1.0
		}

		return float32(reward)
	} else {
		atomic.AddUint64(&a.totalCollisions, 1)
		log.Printf("💥 [飞机 %s] 发送报文 (ID: %s) 时失败(碰撞)。", a.CurrentFlightID, msg.GetBaseMessage().MessageID)

		a.rlStateMutex.Lock()
		a.lastSendCausedCollision = true
		a.stepsSinceLastCollision = 0
		a.rlStateMutex.Unlock()

		return -30.0
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

	a.waitTimesMutex.Lock()
	a.waitTimes = make([]time.Duration, 0, 100)
	a.waitTimesMutex.Unlock()

	a.outboundMutex.Lock()
	a.outboundQueue = make([]outboxItem, 0, 10)
	a.outboundMutex.Unlock()

	a.ackWaiters.Range(func(key, value interface{}) bool {
		a.ackWaiters.Delete(key)
		return true
	})

	a.rlStateMutex.Lock()
	defer a.rlStateMutex.Unlock()
	const sequenceLength = 10
	a.lastSendCausedCollision = false
	a.channelBusyHistory = make([]bool, 0, sequenceLength)
	a.consecutiveIdleSteps = 0
	a.stepsSinceLastCollision = 0
	a.packetWaitingSteps = 0
}

// GetWaitTimes 返回一个线程安全的等待时间副本
func (a *Aircraft) GetWaitTimes() []time.Duration {
	a.waitTimesMutex.Lock()
	defer a.waitTimesMutex.Unlock()
	timesCopy := make([]time.Duration, len(a.waitTimes))
	copy(timesCopy, a.waitTimes)
	return timesCopy
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
