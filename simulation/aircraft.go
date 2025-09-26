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
	inboundQueue chan ACARSMessageInterface // 自己的消息收件箱
	outbox       chan OutboxItem            // 自己的消息发件箱
	ackWaiters   sync.Map

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
		outbox:                  make(chan OutboxItem, 100),           // 初始化发件箱
		ackWaiters:              sync.Map{},                           // 初始时间
	}
}

// EnqueueMessage 将消息放入发件箱以供发送
func (a *Aircraft) EnqueueMessage(msg ACARSMessageInterface) {
	item := OutboxItem{
		Message:     msg,
		EnqueueTime: time.Now(),
	}
	a.outbox <- item
	log.Printf("📥 [飞机 %s] 新报文 (ID: %s) 已加入发件箱。", a.CurrentFlightID, msg.GetBaseMessage().MessageID)
}

// ProcessOutbox 循环并串行处理发件箱中的消息
func (a *Aircraft) ProcessOutbox(comms *CommunicationSystem) {
	for item := range a.outbox {
		// 确保一次只发送一封邮件，因此这里是同步调用
		a.SendMessage(item, comms)
	}
}

func (a *Aircraft) StartListening(comms *CommunicationSystem) {
	comms.RegisterListener(a.inboundQueue) // 通过管理器注册
	log.Printf("✈️  [飞机 %s] 的通信系统已启动，开始监听主/备信道...", a.CurrentFlightID)

	go a.ProcessOutbox(comms) // 在单独的 goroutine 中启动发件箱处理器

	for msg := range a.inboundQueue {
		// 只关心 ACK 报文
		if msg.GetBaseMessage().Type != MsgTypeAck {
			continue
		}
		// 尝试解析 ACK 数据
		var ackData AcknowledgementData
		// GetData() 返回的是 json.RawMessage，需要先转换
		if rawData, ok := msg.GetData().(json.RawMessage); ok {
			if err := json.Unmarshal(rawData, &ackData); err != nil {
				continue // 解析失败，忽略
			}
		} else {
			continue
		}

		// 检查这个 ACK 是否是我们正在等待的
		if waiterChan, ok := a.ackWaiters.Load(ackData.OriginalMessageID); ok {
			log.Printf("🎉 [飞机 %s] 成功收到对报文 %s 的 ACK!", a.CurrentFlightID, ackData.OriginalMessageID)
			// 发送信号，通知等待的 goroutine
			waiterChan.(chan bool) <- true
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

		// --- 核心逻辑: 在每次重试前，都动态选择信道 ---
		targetChannel := comms.SelectChannelForMessage(msg, a.CurrentFlightID)
		p := targetChannel.GetPForMessage(msg.GetPriority())
		// 2. 从选定的目标信道获取其专属的时隙
		timeSlotForChannel := targetChannel.GetCurrentTimeSlot()

		// 在选定的目标信道上执行 p-坚持 CSMA 算法

		for {
			atomic.AddUint64(&a.totalRqTunnel, 1)
			if !targetChannel.IsBusy() {
				if rand.Float64() < p {
					// 只有在概率允许时才真正尝试传输，这构成一次“传输尝试”
					atomic.AddUint64(&a.totalTxAttempts, 1)
					if targetChannel.AttemptTransmit(msg, a.CurrentFlightID, config.TransmissionTime) {
						// 传输成功，记录从入队到成功抢占信道的总等待时间
						waitTime := time.Since(enqueueTime)
						a.totalWaitTimeNs.Add(waitTime.Nanoseconds())
						log.Printf("🚀 [飞机 %s] 成功发送报文 (ID: %s, Prio: %s)，耗时: %v", a.CurrentFlightID, baseMsg.MessageID, msg.GetPriority(), waitTime)
						// 跳出CSMA循环，去等待ACK
						goto waitForAck
					} else {
						// 传输失败，即发生碰撞
						atomic.AddUint64(&a.totalCollisions, 1)
						// 4. 日志增强: 明确指出在哪个信道上发生了碰撞
						log.Printf("💥 [飞机 %s] 在信道 [%s] 上发生碰撞！", a.CurrentFlightID, targetChannel.ID)
					}
				} else {
					// 4. 日志增强: 明确指出在哪个信道上延迟
					log.Printf("🤔 [飞机 %s] 在信道 [%s] 上空闲，但决定延迟 (p=%.2f)。", a.CurrentFlightID, targetChannel.ID, p)
				}
			} else {
				atomic.AddUint64(&a.totalFailRqTunnel, 1)
				// 4. 日志增强: 明确指出哪个信道忙
				log.Printf("⏳ [飞机 %s] 发现信道 [%s] 忙，持续监听...", a.CurrentFlightID, targetChannel.ID)
			}
			// 3. 使用从信道获取的专属时隙进行等待
			time.Sleep(timeSlotForChannel)
		}

	waitForAck:
		// 等待 ACK 或超时的逻辑保持不变
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
			log.Printf("⏰ [飞机 %s] 等待报文 (ID: %s) 的 ACK 超时！准备重发...", a.CurrentFlightID, baseMsg.MessageID)
		}
	}

	log.Printf("❌ [飞机 %s] 报文 (ID: %s) 发送失败，已达到最大重试次数。", a.CurrentFlightID, baseMsg.MessageID)
}

func (a *Aircraft) ResetStats() {
	atomic.StoreUint64(&a.totalTxAttempts, 0)
	atomic.StoreUint64(&a.totalCollisions, 0)
	atomic.StoreUint64(&a.successfulTx, 0)
	atomic.StoreUint64(&a.totalRetries, 0)
	a.totalWaitTimeNs.Store(0)
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
}

func (a *Aircraft) GetRawStats() AircraftRawStats {
	return AircraftRawStats{
		SuccessfulTx:      atomic.LoadUint64(&a.successfulTx),
		TotalTxAttempts:   atomic.LoadUint64(&a.totalTxAttempts),
		TotalCollisions:   atomic.LoadUint64(&a.totalCollisions),
		TotalRetries:      atomic.LoadUint64(&a.totalRetries),
		TotalRqTunnel:     atomic.LoadUint64(&a.totalRqTunnel),
		TotalFailRqTunnel: atomic.LoadUint64(&a.totalFailRqTunnel),
		TotalWaitTime:     time.Duration(a.totalWaitTimeNs.Load()),
	}
}
