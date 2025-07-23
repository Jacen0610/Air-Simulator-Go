package simulation

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

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
		ackWaiters:              sync.Map{},                           // 初始时间
	}
}

func (a *Aircraft) StartListening(comms *CommunicationSystem) {
	comms.RegisterListener(a.inboundQueue) // 通过管理器注册
	log.Printf("✈️  [飞机 %s] 的通信系统已启动，开始监听主/备信道...", a.CurrentFlightID)

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

type PriorityPMap map[Priority]float64

// SendMessage 实现了一个带 p-坚持 CSMA 和 ACK/重传机制的完整发送流程。
func (a *Aircraft) SendMessage(msg ACARSMessageInterface, comms *CommunicationSystem, timeSlot time.Duration) {
	baseMsg := msg.GetBaseMessage()
	sendStartTime := time.Now()

	for retries := 0; retries < MaxRetries; retries++ {
		log.Printf("🚀 [飞机 %s] 准备发送报文 (ID: %s), 尝试次数: %d/%d", a.CurrentFlightID, baseMsg.MessageID, retries+1, MaxRetries)
		if retries > 0 {
			atomic.AddUint64(&a.totalRetries, 1)
		}

		// --- 核心修改: 在每次尝试发送前，都动态选择信道 ---
		targetChannel := comms.SelectChannelForMessage(msg, a.CurrentFlightID)
		p := targetChannel.GetPForMessage(msg.GetPriority())

		// 1. 在选定的目标信道上执行 p-坚持 CSMA 算法
		for {
			atomic.AddUint64(&a.totalRqTunnel, 1)
			if !targetChannel.IsBusy() {
				if rand.Float64() < p {
					if targetChannel.AttemptTransmit(msg, a.CurrentFlightID, TransmissionTime) {
						waitTime := time.Since(sendStartTime)
						a.totalWaitTimeNs.Add(waitTime.Nanoseconds())
						atomic.AddUint64(&a.totalTxAttempts, 1)
						goto waitForAck
					} else {
						atomic.AddUint64(&a.totalTxAttempts, 1)
						atomic.AddUint64(&a.totalCollisions, 1)
						log.Printf("💥 [飞机 %s] 在信道上碰撞！", a.CurrentFlightID)
					}
				} else {
					log.Printf("🤔 [飞机 %s] 信道空闲，但决定延迟 (p=%.2f)。等待下一个时隙...", a.CurrentFlightID, p)
				}
			} else {
				atomic.AddUint64(&a.totalFailRqTunnel, 1)
				log.Printf("⏳ [飞机 %s] 信道忙，持续监听...", a.CurrentFlightID)
			}
			time.Sleep(timeSlot)
		}

	waitForAck:
		// 2. 等待 ACK 或超时的逻辑保持不变
		ackChan := make(chan bool, 1)
		a.ackWaiters.Store(baseMsg.MessageID, ackChan)

		select {
		case <-ackChan:
			atomic.AddUint64(&a.successfulTx, 1)
			a.ackWaiters.Delete(baseMsg.MessageID)
			log.Printf("✅ [飞机 %s] 报文 (ID: %s) 发送流程完成！", a.CurrentFlightID, baseMsg.MessageID)
			return
		case <-time.After(AckTimeout):
			a.ackWaiters.Delete(baseMsg.MessageID)
			log.Printf("⏰ [飞机 %s] 等待报文 (ID: %s) 的 ACK 超时！准备重发...", a.CurrentFlightID, baseMsg.MessageID)
		}
	}

	log.Printf("❌ [飞机 %s] 报文 (ID: %s) 发送失败，已达到最大重试次数。", a.CurrentFlightID, baseMsg.MessageID)
}

// UpdatePosition 更新航空器的位置信息
func (a *Aircraft) UpdatePosition(lat, lon, alt, speed, heading float64) {
	a.CurrentPosition = &PositionReportData{
		Latitude:  lat,
		Longitude: lon,
		Altitude:  alt,
		Speed:     speed,
		Heading:   heading,
		Timestamp: time.Now(),
	}
	a.LastDataReportTimestamp = time.Now()
}

// UpdateFuel 更新航空器的燃油信息
func (a *Aircraft) UpdateFuel(remainingKG, consumptionRateKGPH float64) {
	a.FuelRemainingKG = remainingKG
	a.FuelConsumptionRateKGPH = consumptionRateKGPH
	a.LastDataReportTimestamp = time.Now()
}

// UpdateEngineStatus 更新特定发动机的状态
func (a *Aircraft) UpdateEngineStatus(engineID int, n1, egt, fuelFlow, oilPressure float64, flightPhase string) {
	a.EngineStatus[engineID] = &EngineReportData{
		EngineID:      engineID,
		N1RPM:         n1,
		EGT:           egt,
		FuelFlow:      fuelFlow,
		OilPressure:   oilPressure,
		FlightPhase:   flightPhase,
		ReportTimeUTC: time.Now().UTC(),
	}
	a.LastDataReportTimestamp = time.Now()
}

// UpdateOOOIReport 更新 OOOI 报告
func (a *Aircraft) UpdateOOOIReport(out, off, on, in time.Time, origin, dest string) {
	a.LastOOOIReport = &OOOIReportData{
		OutTime: out,
		OffTime: off,
		OnTime:  on,
		InTime:  in,
		Origin:  origin,
		Dest:    dest,
	}
	a.LastDataReportTimestamp = time.Now()
}

// GetCommunicationStats 计算并返回一个包含通信统计信息的可读字符串。
func (a *Aircraft) GetCommunicationStats() string {
	// 使用 atomic.LoadUint64 来安全地读取计数值
	attempts := atomic.LoadUint64(&a.totalTxAttempts)
	collisions := atomic.LoadUint64(&a.totalCollisions)
	successes := atomic.LoadUint64(&a.successfulTx)
	retries := atomic.LoadUint64(&a.totalRetries)
	totalWaitNs := a.totalWaitTimeNs.Load()

	var avgWaitTime time.Duration
	if successes > 0 {
		avgWaitTime = time.Duration(totalWaitNs / int64(successes+retries))
	}

	var collisionRate float64
	if attempts > 0 {
		collisionRate = (float64(collisions) / float64(attempts)) * 100
	}

	stats := fmt.Sprintf("--- 通信统计 for 飞机 %s ---\n", a.CurrentFlightID)
	stats += fmt.Sprintf("  - 成功发送报文数: %d\n", successes)
	stats += fmt.Sprintf("  - 总传输尝试次数: %d\n", attempts)
	stats += fmt.Sprintf("  - 碰撞/信道访问失败次数: %d\n", collisions)
	stats += fmt.Sprintf("  - 总重传次数: %d\n", retries)
	stats += fmt.Sprintf("  - 碰撞率 (失败/尝试): %.2f%%\n", collisionRate)

	stats += fmt.Sprintf("  - 平均等待时间 (成功发送): %v\n", avgWaitTime.Round(time.Millisecond)) // 新增
	stats += "--------------------------------------\n"

	return stats
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
