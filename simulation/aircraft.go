package simulation

import (
	"Air-Simulator/config"
	"encoding/json"
	"log"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"
)

// OutboxItem 持有消息及其入队时间
type OutboxItem struct {
	Message     ACARSMessageInterface
	EnqueueTime time.Time
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
	inboundQueue chan ACARSMessageInterface
	outbox       chan OutboxItem
	ackWaiters   sync.Map
	metricsChan  chan<- time.Duration
	stopCh       chan struct{}  // 新增: 用于发送停止信号的通道
	wg           sync.WaitGroup // 新增: 用于等待goroutine结束

	// --- 通信统计 ---
	totalTxAttempts      uint64
	totalCollisions      uint64
	successfulTx         uint64
	totalRetries         uint64
	totalDroppedMessages uint64
	totalRqTunnel        uint64
	totalFailRqTunnel    uint64
	totalWaitTimeNs      atomic.Int64
}

// NewAircraft 创建一个航空器实例的构造函数
func NewAircraft(icaoAddr, reg, aircraftType, manufacturer, serialNum, airlineCode string, metricsChan chan<- time.Duration) *Aircraft {
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
		outbox:                  make(chan OutboxItem, 100),
		ackWaiters:              sync.Map{},
		metricsChan:             metricsChan,
		stopCh:                  make(chan struct{}), // 初始化stopCh
	}
}

// Stop 优雅地停止飞机的后台处理goroutine
func (a *Aircraft) Stop() {
	log.Printf("🛑 [飞机 %s] 正在停止...", a.CurrentFlightID)
	close(a.stopCh) // 关闭通道，发送停止信号
	a.wg.Wait()     // 等待所有goroutine完成
	log.Printf("🛑 [飞机 %s] 已成功停止。", a.CurrentFlightID)
}

// EnqueueMessage 将消息放入发件箱以供发送
func (a *Aircraft) EnqueueMessage(msg ACARSMessageInterface) {
	select {
	case a.outbox <- OutboxItem{Message: msg, EnqueueTime: time.Now()}:
		log.Printf("📥 [飞机 %s] 新报文 (ID: %s) 已加入发件箱。", a.CurrentFlightID, msg.GetBaseMessage().MessageID)
	case <-a.stopCh:
		log.Printf("⚠️ [飞机 %s] 无法加入新报文 (ID: %s)，因为飞机正在停止。", a.CurrentFlightID, msg.GetBaseMessage().MessageID)
	}
}

// ProcessOutbox 循环并串行处理发件箱中的消息
func (a *Aircraft) ProcessOutbox(comms *CommunicationSystem) {
	defer a.wg.Done() // 确保goroutine退出时通知WaitGroup
	for {
		select {
		case item := <-a.outbox:
			// 确保一次只发送一封邮件，因此这里是同步调用
			a.SendMessage(item, comms)
		case <-a.stopCh:
			log.Printf("✈️  [飞机 %s] 发件箱处理程序已停止。", a.CurrentFlightID)
			return
		}
	}
}

func (a *Aircraft) StartListening(comms *CommunicationSystem) {
	comms.RegisterListener(a.inboundQueue)
	log.Printf("✈️  [飞机 %s] 的通信系统已启动，开始监听...", a.CurrentFlightID)

	a.wg.Add(1) // 在启动goroutine前增加计数
	go a.ProcessOutbox(comms)

	// 监听入站消息的循环也需要能被停止
	for {
		select {
		case msg := <-a.inboundQueue:
			if msg == nil { // 通道已关闭
				return
			}
			// 只关心 ACK 报文
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

			if waiterChan, ok := a.ackWaiters.Load(ackData.OriginalMessageID); ok {
				log.Printf("🎉 [飞机 %s] 成功收到对报文 %s 的 ACK!", a.CurrentFlightID, ackData.OriginalMessageID)
				select {
				case waiterChan.(chan bool) <- true:
				default:
				}
			}
		case <-a.stopCh:
			log.Printf("✈️  [飞机 %s] 入站监听程序已停止。", a.CurrentFlightID)
			return
		}
	}
}

// SendMessage 从发件箱取出一个项目并尝试发送
func (a *Aircraft) SendMessage(item OutboxItem, comms *CommunicationSystem) {
	msg := item.Message
	enqueueTime := item.EnqueueTime
	baseMsg := msg.GetBaseMessage()

	for retries := 0; retries < config.MaxRetries; retries++ {
		log.Printf("🚀 [飞机 %s] 准备发送报文 (ID: %s, Prio: %s), 尝试次数: %d/%d", a.CurrentFlightID, baseMsg.MessageID, msg.GetPriority(), retries+1, config.MaxRetries)
		if retries > 0 {
			atomic.AddUint64(&a.totalRetries, 1)
		}

		targetChannel := comms.SelectChannelForMessage(msg, a.CurrentFlightID)
		p := config.CSMA_PLAINE
		timeSlotForChannel := targetChannel.GetCurrentTimeSlot()

		for {
			// 增加检查，如果飞机已经停止，则中断发送尝试
			select {
			case <-a.stopCh:
				log.Printf("🛑 [飞机 %s] 发送被中断，因为飞机正在停止。", a.CurrentFlightID)
				return
			default:
				// 继续执行
			}

			atomic.AddUint64(&a.totalRqTunnel, 1)
			if !targetChannel.IsBusy() {
				if rand.Float64() < p {
					atomic.AddUint64(&a.totalTxAttempts, 1)
					if targetChannel.AttemptTransmit(msg, a.CurrentFlightID, config.TransmissionTime) {
						waitTime := time.Since(enqueueTime)
						a.totalWaitTimeNs.Add(waitTime.Nanoseconds())
						if a.metricsChan != nil {
							select {
							case a.metricsChan <- waitTime:
							default:
								log.Printf("⚠️ [飞机 %s] 指标通道已满，本次耗时 %v 未能记录", a.CurrentFlightID, waitTime)
							}
						}
						log.Printf("🚀 [飞机 %s] 成功发送报文 (ID: %s, Prio: %s)，耗时: %v", a.CurrentFlightID, baseMsg.MessageID, msg.GetPriority(), waitTime)
						goto waitForAck
					} else {
						atomic.AddUint64(&a.totalCollisions, 1)
						log.Printf("💥 [飞机 %s] 在信道 [%s] 上发生碰撞！", a.CurrentFlightID, targetChannel.ID)
					}
				} else {
					log.Printf("🤔 [飞机 %s] 在信道 [%s] 上空闲，但决定延迟 (p=%.2f)。", a.CurrentFlightID, targetChannel.ID, p)
				}
			} else {
				atomic.AddUint64(&a.totalFailRqTunnel, 1)
				log.Printf("⏳ [飞机 %s] 发现信道 [%s] 忙，持续监听...", a.CurrentFlightID, targetChannel.ID)
			}
			time.Sleep(timeSlotForChannel)
		}

	waitForAck:
		ackChan := make(chan bool, 1)
		a.ackWaiters.Store(baseMsg.MessageID, ackChan)

		select {
		case <-ackChan:
			atomic.AddUint64(&a.successfulTx, 1)
			a.ackWaiters.Delete(baseMsg.MessageID)
			log.Printf("✅ [飞机 %s] 报文 (ID: %s) 发送流程完成！", a.CurrentFlightID, baseMsg.MessageID)
			return
		case <-time.After(config.AckTimeout):
			a.ackWaiters.Delete(baseMsg.MessageID)
			enqueueTime = time.Now()
			log.Printf("⏰ [飞机 %s] 等待报文 (ID: %s) 的 ACK 超时！准备重发...", a.CurrentFlightID, baseMsg.MessageID)
		case <-a.stopCh:
			log.Printf("🛑 [飞机 %s] 等待ACK被中断，因为飞机正在停止。", a.CurrentFlightID)
			return
		}
	}

	log.Printf("❌ [飞机 %s] 报文 (ID: %s) 发送失败，已达到最大重试次数。", a.CurrentFlightID, baseMsg.MessageID)
	atomic.AddUint64(&a.totalDroppedMessages, 1)
}

func (a *Aircraft) ResetStats() {
	atomic.StoreUint64(&a.totalTxAttempts, 0)
	atomic.StoreUint64(&a.totalCollisions, 0)
	atomic.StoreUint64(&a.successfulTx, 0)
	atomic.StoreUint64(&a.totalRetries, 0)
	atomic.StoreUint64(&a.totalDroppedMessages, 0)
	a.totalWaitTimeNs.Store(0)
}

// AircraftRawStats Excel自动统计需要以下两个函数
type AircraftRawStats struct {
	SuccessfulTx         uint64
	TotalTxAttempts      uint64
	TotalCollisions      uint64
	TotalRetries         uint64
	TotalDroppedMessages uint64
	TotalRqTunnel        uint64
	TotalFailRqTunnel    uint64
	TotalWaitTime        time.Duration
}

func (a *Aircraft) GetRawStats() AircraftRawStats {
	return AircraftRawStats{
		SuccessfulTx:         atomic.LoadUint64(&a.successfulTx),
		TotalTxAttempts:      atomic.LoadUint64(&a.totalTxAttempts),
		TotalCollisions:      atomic.LoadUint64(&a.totalCollisions),
		TotalRetries:         atomic.LoadUint64(&a.totalRetries),
		TotalDroppedMessages: atomic.LoadUint64(&a.totalDroppedMessages),
		TotalRqTunnel:        atomic.LoadUint64(&a.totalRqTunnel),
		TotalFailRqTunnel:    atomic.LoadUint64(&a.totalFailRqTunnel),
		TotalWaitTime:        time.Duration(a.totalWaitTimeNs.Load()),
	}
}
