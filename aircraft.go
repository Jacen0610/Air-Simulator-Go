package main

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
	totalTxAttempts uint64 // 总传输尝试次数 (每次尝试获得信道)
	totalCollisions uint64 // 碰撞/信道访问失败次数
	successfulTx    uint64 // 成功发送并收到ACK的报文总数
	totalRetries    uint64 // 总重传次数
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

func (a *Aircraft) StartListening(commsChannel *Channel) {
	commsChannel.RegisterListener(a.inboundQueue)
	log.Printf("✈️  [飞机 %s] 的通信系统已启动，开始监听信道...", a.CurrentFlightID)

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

const (
	TransmissionTime = 80 * time.Millisecond
	AckTimeout       = 3000 * time.Millisecond
	MaxRetries       = 16
)

// SendMessage 实现了一个带 p-坚持 CSMA 和 ACK/重传机制的完整发送流程。
func (a *Aircraft) SendMessage(msg ACARSMessageInterface, commsChannel *Channel, pMap PriorityPMap, timeSlot time.Duration) {
	baseMsg := msg.GetBaseMessage()
	p := pMap[msg.GetPriority()]

	for retries := 0; retries < MaxRetries; retries++ {
		log.Printf("🚀 [飞机 %s] 准备发送报文 (ID: %s), 尝试次数: %d/%d", a.CurrentFlightID, baseMsg.MessageID, retries+1, MaxRetries)
		if retries > 0 {
			// 这是一个重传
			atomic.AddUint64(&a.totalRetries, 1) // <-- 统计点：记录重传
		}
		// 1. 执行 p-坚持 CSMA 算法来获得发送机会
		for {

			atomic.AddUint64(&a.totalTxAttempts, 1)

			if !commsChannel.IsBusy() {
				// 信道空闲，根据概率 p 决定是否发送
				if rand.Float64() < p {
					// 成功掷骰子，尝试发送
					if commsChannel.AttemptTransmit(msg, a.CurrentFlightID, TransmissionTime) {
						goto waitForAck // 发送已开始，跳出循环去等待 ACK
					}
					// 如果 AttemptTransmit 失败（极小概率的竞态），则继续循环
				} else {

					log.Printf("🤔 [飞机 %s] 信道空闲，但决定延迟 (p=%.2f)。等待下一个时隙...", a.CurrentFlightID, p)
				}
			} else {

				// 信道忙，这是一次明确的碰撞
				atomic.AddUint64(&a.totalCollisions, 1)

				log.Printf("⏳ [飞机 %s] 信道忙，持续监听...", a.CurrentFlightID)
			}
			// 等待一个时隙后重试
			time.Sleep(timeSlot)
		}

	waitForAck:
		// 2. 等待 ACK 或超时
		ackChan := make(chan bool, 1)
		a.ackWaiters.Store(baseMsg.MessageID, ackChan)

		select {
		case <-ackChan:
			// 成功收到 ACK
			atomic.AddUint64(&a.successfulTx, 1)

			a.ackWaiters.Delete(baseMsg.MessageID) // 清理等待者
			log.Printf("✅ [飞机 %s] 报文 (ID: %s) 发送流程完成！", a.CurrentFlightID, baseMsg.MessageID)
			return // 任务完成，退出函数

		case <-time.After(AckTimeout):
			// ACK 超时
			a.ackWaiters.Delete(baseMsg.MessageID) // 清理等待者
			log.Printf("⏰ [飞机 %s] 等待报文 (ID: %s) 的 ACK 超时！准备重发...", a.CurrentFlightID, baseMsg.MessageID)
			// 让 for 循环继续，进入下一次重试
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

// GetInfo 打印航空器简要信息
func (a *Aircraft) GetInfo() string {
	info := fmt.Sprintf("飞机 %s (%s) - %s %s\n", a.Registration, a.ICAOAddress, a.Manufacturer, a.AircraftType)
	info += fmt.Sprintf("  当前航班: %s, 飞行阶段: %s\n", a.CurrentFlightID, a.CurrentFlightPhase)
	if a.CurrentPosition != nil {
		info += fmt.Sprintf("  当前位置: 纬度 %.4f, 经度 %.4f, 高度 %.0fft, 速度 %.0fkt\n",
			a.CurrentPosition.Latitude, a.CurrentPosition.Longitude, a.CurrentPosition.Altitude, a.CurrentPosition.Speed)
	}
	info += fmt.Sprintf("  剩余燃油: %.2f KG, 消耗率: %.2f KG/H\n", a.FuelRemainingKG, a.FuelConsumptionRateKGPH)
	info += fmt.Sprintf("  ACARS Enabled: %t, CPDLC Enabled: %t\n", a.ACARSEnabled, a.CPDLCEnabled)
	return info
}

// GetCommunicationStats 计算并返回一个包含通信统计信息的可读字符串。
func (a *Aircraft) GetCommunicationStats() string {
	// 使用 atomic.LoadUint64 来安全地读取计数值
	attempts := atomic.LoadUint64(&a.totalTxAttempts)
	collisions := atomic.LoadUint64(&a.totalCollisions)
	successes := atomic.LoadUint64(&a.successfulTx)
	retries := atomic.LoadUint64(&a.totalRetries)

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
	stats += "--------------------------------------\n"

	return stats
}
