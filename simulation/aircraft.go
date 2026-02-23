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

// channelStateEvent is used to record the history of channel states for ratio calculations.
type channelStateEvent struct {
	timestamp time.Time
	isBusy    bool
}

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

// [修改] CollisionRecord 记录单次碰撞的详细信息
type CollisionRecord struct {
	EpisodeID       int           // [新增] Episode 编号
	TimeOffset      time.Duration // [新增] 相对于 Episode 开始的时间偏移量
	TrafficMode     string
	TrafficInterval time.Duration
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
	lastAction          AgentAction         // 9. last_act
	lastStepTime        time.Time           // 12. dt_step
	lastStateChangeTime time.Time           // 3, 4. busy_dur, idle_dur
	isCurrentlyBusy     bool                // 3, 4. busy_dur, idle_dur
	channelStateHistory []channelStateEvent // 5, 6. ratio_1s, ratio_01s
	lastCollision       bool                // 10. is_coll
	rlStateMutex        sync.RWMutex

	// [新增] 独立的随机数生成器
	rng *rand.Rand // 用于控制发送时的微观随机延迟 (Offset: 3)

	// [新增] 标志位：是否是第一次重置
	isFirstReset bool

	// [新增] 碰撞记录
	collisionRecords      []CollisionRecord
	collisionRecordsMutex sync.Mutex

	// [新增] 无效动作记录
	invalidActionRecords      []CollisionRecord
	invalidActionRecordsMutex sync.Mutex

	// [新增] 获取背景流量状态的回调函数
	trafficStatusProvider func() (TrafficMode, time.Duration)

	// [新增] 记录当前 Episode 信息
	currentEpisodeID int
	simStartTime     time.Time
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
		// [新增] 初始化独立的RNG
		rng: config.NewRand(3),
		// [新增] 初始化标志位
		isFirstReset:         true,
		collisionRecords:     make([]CollisionRecord, 0),
		invalidActionRecords: make([]CollisionRecord, 0),
	}
	// 注意：NewAircraft 不再直接调用 Reset，因为 Reset 现在需要参数。
	// 初始化时可以给一个默认值，或者由 Server 在第一次 Reset 时正确设置。
	// 为了保持一致性，这里可以调用一个内部的 initReset，或者暂时不调用，等待 Server 调用。
	// 考虑到 Server 会在启动时调用 Reset，这里不调用也是安全的。
	// 但为了防止未初始化状态，我们可以手动初始化一些字段。
	a.rlStateMutex.Lock()
	a.channelStateHistory = make([]channelStateEvent, 0, 100)
	a.rlStateMutex.Unlock()
	return a
}

// [新增] SetTrafficStatusProvider 设置获取背景流量状态的回调函数
func (a *Aircraft) SetTrafficStatusProvider(provider func() (TrafficMode, time.Duration)) {
	a.trafficStatusProvider = provider
}

// [新增] GetCollisionRecords 获取所有的碰撞记录
func (a *Aircraft) GetCollisionRecords() []CollisionRecord {
	a.collisionRecordsMutex.Lock()
	defer a.collisionRecordsMutex.Unlock()
	// 返回副本以保证线程安全
	records := make([]CollisionRecord, len(a.collisionRecords))
	copy(records, a.collisionRecords)
	return records
}

// [新增] GetInvalidActionRecords 获取所有的无效动作记录
func (a *Aircraft) GetInvalidActionRecords() []CollisionRecord {
	a.invalidActionRecordsMutex.Lock()
	defer a.invalidActionRecordsMutex.Unlock()
	// 返回副本以保证线程安全
	records := make([]CollisionRecord, len(a.invalidActionRecords))
	copy(records, a.invalidActionRecords)
	return records
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

// [核心重构] GetObservation - 计算并返回12维观测状态
func (a *Aircraft) GetObservation(comms *CommunicationSystem, simStartTime time.Time) AgentObservation {
	now := time.Now()

	// --- 快速获取并释放锁，将需要的数据复制到局部变量 ---
	a.outboundMutex.RLock()
	queueLen := len(a.outboundQueue)
	var topMsgEnqueueTime time.Time
	if queueLen > 0 {
		topMsgEnqueueTime = a.outboundQueue[0].enqueueTime
	}
	a.outboundMutex.RUnlock()

	a.rlStateMutex.RLock()
	lastAct := a.lastAction
	isColl := a.lastCollision
	dtStep := float32(now.Sub(a.lastStepTime).Seconds())
	busyDur := float32(0)
	idleDur := float32(0)
	if a.isCurrentlyBusy {
		busyDur = float32(now.Sub(a.lastStateChangeTime).Seconds())
	} else {
		idleDur = float32(now.Sub(a.lastStateChangeTime).Seconds())
	}
	history := make([]channelStateEvent, len(a.channelStateHistory))
	copy(history, a.channelStateHistory)
	a.rlStateMutex.RUnlock()

	// --- 使用局部变量进行后续计算 ---

	// 1. has_data
	hasData := float32(0)
	if queueLen > 0 {
		hasData = 1.0
	}

	// 2. is_busy
	isBusy := float32(0)
	if comms.PrimaryChannel.IsBusy() {
		isBusy = 1.0
	}

	// 3. busy_dur (归一化)
	if busyDur > 1.0 {
		busyDur = 1.0
	}

	// 4. idle_dur (归一化)
	if idleDur > 1.0 {
		idleDur = 1.0
	}

	// 5. ratio_1s & 6. ratio_01s
	ratio1s := calculateBusyRatio(history, now, 1*time.Second)
	ratio01s := calculateBusyRatio(history, now, 100*time.Millisecond)

	// 7. wait_time (归一化)
	waitTime := float32(0)
	if queueLen > 0 {
		waitTime = float32(now.Sub(topMsgEnqueueTime).Seconds())
	}
	if waitTime > 10.0 {
		waitTime = 10.0
	}
	waitTime /= 10.0

	// 8. q_size (归一化)
	qSize := float32(queueLen)
	if qSize > 10.0 {
		qSize = 10.0
	}
	qSize /= 10.0

	// 9. last_act
	lastActFloat := float32(lastAct)

	// 10. is_coll
	isCollFloat := float32(0)
	if isColl {
		isCollFloat = 1.0
	}

	// 11. cycle_pos (归一化)
	cyclePos := float32(math.Mod(now.Sub(simStartTime).Seconds(), 360.0) / 360.0)

	dtStep = min(dtStep/0.01, 1.0)

	return AgentObservation{
		HasData:  hasData,
		IsBusy:   isBusy,
		BusyDur:  busyDur,
		IdleDur:  idleDur,
		Ratio1s:  ratio1s,
		Ratio01s: ratio01s,
		WaitTime: waitTime,
		QSize:    qSize,
		LastAct:  lastActFloat,
		IsColl:   isCollFloat,
		CyclePos: cyclePos,
		DtStep:   dtStep,
	}
}

// calculateBusyRatio is a helper to compute the busy ratio over a given duration.
func calculateBusyRatio(history []channelStateEvent, now time.Time, duration time.Duration) float32 {
	if len(history) == 0 {
		return 0
	}

	startTime := now.Add(-duration)
	var busyTime time.Duration

	// Find the starting point in history
	firstRelevantIndex := -1
	for i, event := range history {
		if event.timestamp.After(startTime) {
			firstRelevantIndex = i
			break
		}
	}

	if firstRelevantIndex == -1 { // All history is older than the duration
		if history[len(history)-1].isBusy {
			return 1.0
		}
		return 0.0
	}

	// Calculate busy time from the relevant history portion
	relevantHistory := history[firstRelevantIndex:]

	// Handle the time between startTime and the first relevant event
	prevEventIndex := firstRelevantIndex - 1
	if prevEventIndex >= 0 && history[prevEventIndex].isBusy {
		busyTime += relevantHistory[0].timestamp.Sub(startTime)
	}

	for i := 0; i < len(relevantHistory)-1; i++ {
		if relevantHistory[i].isBusy {
			busyTime += relevantHistory[i+1].timestamp.Sub(relevantHistory[i].timestamp)
		}
	}

	// Handle the last event up to 'now'
	lastEvent := relevantHistory[len(relevantHistory)-1]
	if lastEvent.isBusy {
		busyTime += now.Sub(lastEvent.timestamp)
	}

	ratio := float64(busyTime) / float64(duration)
	if ratio > 1.0 {
		return 1.0
	}
	return float32(ratio)
}

// updateRLState 在每个 Step 开始时更新状态
func (a *Aircraft) updateRLState(comms *CommunicationSystem) {
	a.rlStateMutex.Lock()
	defer a.rlStateMutex.Unlock()

	now := time.Now()
	isBusy := comms.PrimaryChannel.IsBusy()

	// Update channel state history and durations
	if isBusy != a.isCurrentlyBusy {
		a.lastStateChangeTime = now
		a.isCurrentlyBusy = isBusy
	}

	// Prune history to keep it manageable (e.g., last 2 seconds)
	twoSecondsAgo := now.Add(-2 * time.Second)
	pruneIndex := 0
	for i, event := range a.channelStateHistory {
		if event.timestamp.After(twoSecondsAgo) {
			pruneIndex = i
			break
		}
	}
	if pruneIndex > 0 {
		a.channelStateHistory = a.channelStateHistory[pruneIndex:]
	}
	a.channelStateHistory = append(a.channelStateHistory, channelStateEvent{timestamp: now, isBusy: isBusy})

	// Update dt_step
	a.lastStepTime = now
}

// Step 函数: 执行一步决策并返回奖励
func (a *Aircraft) Step(action AgentAction, comms *CommunicationSystem) float32 {
	// [核心修复] 在Step开始时，总是先重置上一步的碰撞标志
	a.rlStateMutex.Lock()
	a.lastCollision = false
	a.rlStateMutex.Unlock()

	a.updateRLState(comms)

	reward := float32(0)
	itemToSend := a.peekMessage()

	a.rlStateMutex.Lock()
	a.lastAction = action
	a.rlStateMutex.Unlock()

	if itemToSend == nil {
		if action == ActionSend {
			return -5.0 // 无效发送惩罚减半，保持比例
		}
		return 0.001 // 空仓等待不给分，防止摆烂
	}

	switch action {
	case ActionWait:

		const queuePenaltyFactor = 3.0 // 稍微加大
		a.outboundMutex.RLock()
		queueLen := float32(len(a.outboundQueue))
		a.outboundMutex.RUnlock()
		// 使用线性+对数，防止大队列时彻底放弃
		reward -= queuePenaltyFactor * float32(math.Log1p(float64(queueLen)))

		// --- 修正3：激进的等待时间惩罚 ---
		const waitTimeFactor = 5.0 // 显著提升，让 Agent 感到肉疼
		secondsWaiting := float32(time.Since(itemToSend.enqueueTime).Seconds())

		var waitTimePenalty float32
		if secondsWaiting <= 0.5 { // 缩短缓冲期，从 1.0s 降到 0.5s
			waitTimePenalty = waitTimeFactor * secondsWaiting
		} else {
			// 加速惩罚：一旦超过 0.5s，让惩罚迅速积累
			diff := float64(secondsWaiting - 0.5)
			waitTimePenalty = waitTimeFactor * (0.5 + float32(math.Pow(diff, 1.2)))
		}
		reward -= waitTimePenalty

	case ActionSend:
		reward += a.attemptSendOnChannel(itemToSend, comms.PrimaryChannel)
	}
	return reward
}

// attemptSendOnChannel 尝试在指定信道上发送消息
func (a *Aircraft) attemptSendOnChannel(item *outboxItem, channel *Channel) float32 {
	atomic.AddUint64(&a.totalRqTunnel, 1)
	// [修改] 使用全局可控的随机数生成器
	time.Sleep(time.Duration(10+a.rng.Intn(41)) * time.Microsecond)

	if channel.IsBusy() {
		atomic.AddUint64(&a.totalFailRqTunnel, 1)

		// [新增] 记录无效动作事件
		if a.trafficStatusProvider != nil {
			mode, interval := a.trafficStatusProvider()
			a.invalidActionRecordsMutex.Lock()
			a.invalidActionRecords = append(a.invalidActionRecords, CollisionRecord{
				EpisodeID:       a.currentEpisodeID,
				TimeOffset:      time.Since(a.simStartTime),
				TrafficMode:     string(mode),
				TrafficInterval: interval,
			})
			a.invalidActionRecordsMutex.Unlock()
		}

		return -8.0
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

		waitTimeSeconds := waitTime.Seconds()
		optimalTime := 0.4
		var finalReward float64

		// 修正4：压缩成功奖励到 [1, 15] 范围
		if waitTimeSeconds <= optimalTime {
			finalReward = 15.0
		} else {
			// 指数衰减：从 15 掉向 1
			timeDiff := waitTimeSeconds - optimalTime
			exponent := -1.0 * timeDiff * timeDiff // 稍微放宽衰减速度
			finalReward = 14.0*math.Exp(exponent) + 1.0
		}

		// 额外的时效奖金（保持比例）
		if waitTimeSeconds < 0.5 {
			finalReward += 5.0
		}

		return float32(finalReward)
	} else {
		atomic.AddUint64(&a.totalCollisions, 1)
		log.Printf("💥 [飞机 %s] 发送报文 (ID: %s) 时失败(碰撞)。", a.CurrentFlightID, msg.GetBaseMessage().MessageID)

		a.rlStateMutex.Lock()
		a.lastCollision = true
		a.rlStateMutex.Unlock()

		// [新增] 记录碰撞事件
		if a.trafficStatusProvider != nil {
			mode, interval := a.trafficStatusProvider()
			a.collisionRecordsMutex.Lock()
			a.collisionRecords = append(a.collisionRecords, CollisionRecord{
				EpisodeID:       a.currentEpisodeID,
				TimeOffset:      time.Since(a.simStartTime),
				TrafficMode:     string(mode),
				TrafficInterval: interval,
			})
			a.collisionRecordsMutex.Unlock()
		}

		return -15.0
	}
}

// Reset 重置飞机状态
// [修改] 接收 episodeID 和 startTime 参数
func (a *Aircraft) Reset(episodeID int, startTime time.Time) {
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
	now := time.Now()
	a.lastAction = ActionWait // Default action
	a.lastStepTime = now
	a.lastStateChangeTime = now
	a.isCurrentlyBusy = false                                 // Assume channel is initially idle
	a.channelStateHistory = make([]channelStateEvent, 0, 100) // Approx 2s of history at high freq
	a.lastCollision = false

	// [修改] 仅在第一次重置时强制对齐随机数种子
	if a.isFirstReset {
		a.rng = config.NewRand(3)
		a.isFirstReset = false
		log.Println("✈️  [飞机] 首次重置：随机数种子已强制对齐。")
	} else {
		log.Println("✈️  [飞机] 后续重置：保留随机数状态以增加多样性。")
	}

	// [新增] 重置碰撞记录
	a.collisionRecordsMutex.Lock()
	a.collisionRecords = make([]CollisionRecord, 0)
	a.collisionRecordsMutex.Unlock()

	// [新增] 重置无效动作记录
	a.invalidActionRecordsMutex.Lock()
	a.invalidActionRecords = make([]CollisionRecord, 0)
	a.invalidActionRecordsMutex.Unlock()

	// [新增] 更新当前 Episode 信息
	a.currentEpisodeID = episodeID
	a.simStartTime = startTime
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
