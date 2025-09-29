package simulation

import (
	"Air-Simulator/config"
	"fmt"
	"log"
	"sync"
	"time"
)

// FlightPlan 结构体 (无变化)
type FlightPlan struct {
	Aircraft         *Aircraft
	StartTimeMinutes int    // 从模拟开始计算的起飞/进入空域时间 (分钟)
	Type             string // "Departing" (离港), "Arriving" (进港), 或 "Cruising" (巡航)
}

// flightPlans 变量: (无变化)
var flightPlans = []FlightPlan{
	// Departing Flights
	{Type: "Departing", StartTimeMinutes: 1},
	{Type: "Departing", StartTimeMinutes: 3},
	{Type: "Departing", StartTimeMinutes: 6},
	{Type: "Departing", StartTimeMinutes: 11},
	{Type: "Departing", StartTimeMinutes: 15},
	{Type: "Departing", StartTimeMinutes: 16},
	{Type: "Departing", StartTimeMinutes: 19},
	{Type: "Departing", StartTimeMinutes: 23},
	{Type: "Departing", StartTimeMinutes: 25},
	{Type: "Departing", StartTimeMinutes: 28},

	// Arriving Flights
	{Type: "Arriving", StartTimeMinutes: 2},
	{Type: "Arriving", StartTimeMinutes: 6},
	{Type: "Arriving", StartTimeMinutes: 9},
	{Type: "Arriving", StartTimeMinutes: 10},
	{Type: "Arriving", StartTimeMinutes: 13},
	{Type: "Arriving", StartTimeMinutes: 18},
	{Type: "Arriving", StartTimeMinutes: 22},
	{Type: "Arriving", StartTimeMinutes: 24},
	{Type: "Arriving", StartTimeMinutes: 26},
	{Type: "Arriving", StartTimeMinutes: 27},

	// Cruising Flights
	{Type: "Cruising", StartTimeMinutes: 4},
	{Type: "Cruising", StartTimeMinutes: 7},
	{Type: "Cruising", StartTimeMinutes: 12},
	{Type: "Cruising", StartTimeMinutes: 20},
	{Type: "Cruising", StartTimeMinutes: 29},
}

// AircraftCount 会根据 flightPlans 的长度自动更新
var AircraftCount = len(flightPlans)

// RunSimulationSession 更新为接收 CommunicationSystem
func RunSimulationSession(wg *sync.WaitGroup, commsSystem *CommunicationSystem, aircraftList []*Aircraft) {
	// 为飞行计划分配飞机实例
	if len(flightPlans) > len(aircraftList) {
		log.Printf("警告: 飞行计划数量 (%d) 大于飞机实例数量 (%d)。部分计划将不会执行。", len(flightPlans), len(aircraftList))
		return
	}
	for i := range flightPlans {
		flightPlans[i].Aircraft = aircraftList[i]
	}

	// 为每个飞行计划启动一个独立的模拟 goroutine
	for i := range flightPlans {
		wg.Add(1)
		plan := flightPlans[i]
		go simulateFlight(plan, wg)
	}
}

// simulateCruisingPhase 封装了发送巡航报告的通用逻辑
func simulateCruisingPhase(a *Aircraft, duration time.Duration) {
	log.Printf("✈️  [飞机 %s] 进入巡航阶段，将持续 %v...", a.CurrentFlightID, duration)

	posTicker := time.NewTicker(config.PosReportInterval)
	defer posTicker.Stop()
	fuelTicker := time.NewTicker(config.FuelReportInterval)
	defer fuelTicker.Stop()
	weatherTicker := time.NewTicker(config.WeatherReportInterval)
	defer weatherTicker.Stop()
	flightTimer := time.NewTimer(duration)
	defer flightTimer.Stop()

cruisingLoop:
	for {
		select {
		case <-posTicker.C:
			sendPositionReport(a)
		case <-fuelTicker.C:
			sendFuelReport(a)
		case <-weatherTicker.C:
			sendWeatherReport(a)
		case <-flightTimer.C:
			break cruisingLoop
		}
	}
}

// simulateFlight: [修改] 更新了 'Cruising' 计划的逻辑
func simulateFlight(plan FlightPlan, wg *sync.WaitGroup) {
	defer wg.Done()

	// 1. 等待至预定的飞行计划开始时间
	startTime := time.Duration(plan.StartTimeMinutes) * time.Minute
	time.Sleep(startTime)
	log.Printf("🛫 [飞机 %s] 飞行计划启动。类型: %s, 计划开始于 %d 分钟", plan.Aircraft.CurrentFlightID, plan.Type, plan.StartTimeMinutes)

	// 2. 根据飞行计划类型执行不同的通信逻辑
	switch plan.Type {
	case "Departing":
		// 离港飞机流程
		sendOOOIMessage(plan.Aircraft, "OUT", time.Now()) // 推出
		time.Sleep(config.TaxiTime)                       // 滑行
		sendOOOIMessage(plan.Aircraft, "OFF", time.Now()) // 起飞

		// --- 起飞后5分钟，每分钟发送引擎报告 ---
		log.Printf("✈️  [飞机 %s] 进入起飞后初始爬升阶段，将持续报告引擎状况...", plan.Aircraft.CurrentFlightID)
		engineReportTicker := time.NewTicker(1 * time.Minute)
		engineReportTimer := time.NewTimer(5 * time.Minute)
	initialClimbLoop:
		for {
			select {
			case <-engineReportTicker.C:
				sendEngineReport(plan.Aircraft)
			case <-engineReportTimer.C:
				engineReportTicker.Stop()
				break initialClimbLoop
			}
		}
		log.Printf("✈️  [飞机 %s] 初始爬升阶段结束。", plan.Aircraft.CurrentFlightID)

		// 调用通用的巡航阶段函数
		simulateCruisingPhase(plan.Aircraft, config.FlightDuration)

		log.Printf("✈️  [飞机 %s] 已飞出空域。飞行计划结束。", plan.Aircraft.CurrentFlightID)

	case "Arriving":
		// 进港飞机流程
		sendPositionReport(plan.Aircraft) // 进入空域时首先报告位置

		// 调用通用的巡航阶段函数
		simulateCruisingPhase(plan.Aircraft, config.FlightDuration)

		onTime := time.Now()
		sendOOOIMessage(plan.Aircraft, "ON", onTime) // 降落

		// --- 降落后5分钟，每分钟发送引擎报告 ---
		log.Printf("🛬 [飞机 %s] 完成降落，将持续报告引擎反推及冷却状况...", plan.Aircraft.CurrentFlightID)
		engineReportTicker := time.NewTicker(1 * time.Minute)
		engineReportTimer := time.NewTimer(5 * time.Minute)
	landingRollLoop:
		for {
			select {
			case <-engineReportTicker.C:
				sendEngineReport(plan.Aircraft)
			case <-engineReportTimer.C:
				engineReportTicker.Stop()
				break landingRollLoop
			}
		}

		time.Sleep(config.TaxiTime)                  // 滑行至停机位
		sendOOOIMessage(plan.Aircraft, "IN", onTime) // 到达

		log.Printf("🛬 [飞机 %s] 已成功降落并抵达停机位。飞行计划结束。", plan.Aircraft.CurrentFlightID)

	case "Cruising":
		// [核心修改] 'Cruising' 类型的飞机在巡航前后各发送一个ATC报文
		sendATCMessage(plan.Aircraft, "REQUEST CRUISING ALTITUDE FL350")

		simulateCruisingPhase(plan.Aircraft, config.FlightDuration)

		sendATCMessage(plan.Aircraft, "LEAVING SECTOR, CONTACT NEXT CENTER 128.5")

		log.Printf("✈️  [飞机 %s] 已完成巡航任务。飞行计划结束。", plan.Aircraft.CurrentFlightID)

	default:
		log.Printf("❌ [飞机 %s] 遇到未知的飞行计划类型: %s", plan.Aircraft.CurrentFlightID, plan.Type)
	}
}

// --- 各种发送报告的函数 ---

// [新增] sendATCMessage 将 ATC 报文放入飞机的发件箱
func sendATCMessage(a *Aircraft, instruction string) {
	log.Printf("📡 [飞机 %s] 准备发送 ATC 报文: %s", a.CurrentFlightID, instruction)

	// 为 ATC 报文定义一个简单的数据结构
	type ATCMessageData struct {
		Instruction string
		Timestamp   time.Time
	}
	atcData := ATCMessageData{
		Instruction: instruction,
		Timestamp:   time.Now(),
	}

	// 假设 MsgTypeATC 在 message.go 中已定义
	baseMsg := ACARSBaseMessage{
		AircraftICAOAddress: a.ICAOAddress,
		FlightID:            a.CurrentFlightID,
		MessageID:           fmt.Sprintf("%s-ATC-%d", a.CurrentFlightID, time.Now().UnixNano()),
		Type:                "ATC", // 使用字符串 "ATC" 作为类型，假设 message.go 中有对应的 MsgTypeATC
		Timestamp:           time.Now(),
	}

	// ATC 报文通常具有最高优先级
	// 假设 NewCriticalPriorityMessage 构造函数存在
	msg, err := NewCriticalPriorityMessage(baseMsg, atcData)
	if err != nil {
		log.Printf("❌ [飞机 %s] 创建 ATC 报文失败: %v", a.CurrentFlightID, err)
		return
	}
	a.EnqueueMessage(msg)
}

// sendEngineReport 将报告放入飞机的发件箱
func sendEngineReport(a *Aircraft) {
	log.Printf("📡 [飞机 %s] 准备发送引擎报告...", a.CurrentFlightID)
	engineData := EngineReportData{
		EngineID: 1, N1RPM: 85.5, EGT: 450, FuelFlow: 1200, OilPressure: 75,
		FlightPhase: "CLIMB", ReportTimeUTC: time.Now().UTC(),
	}
	baseMsg := ACARSBaseMessage{
		AircraftICAOAddress: a.ICAOAddress, FlightID: a.CurrentFlightID,
		MessageID: fmt.Sprintf("%s-ENG-%d", a.CurrentFlightID, time.Now().Unix()),
		Type:      MsgTypeEngineReport,
		Timestamp: time.Now(),
	}
	msg, _ := NewMediumLowPriorityMessage(baseMsg, engineData)
	a.EnqueueMessage(msg)
}

// sendFuelReport 将报告放入飞机的发件箱
func sendFuelReport(a *Aircraft) {
	log.Printf("📡 [飞机 %s] 准备发送燃油报告...", a.CurrentFlightID)
	fuelData := FuelReportData{
		RemainingFuelKG: 12000.0, FuelFlowKGPH: 200.0, EstimatedTime: time.Now(),
	}
	baseMsg := ACARSBaseMessage{
		AircraftICAOAddress: a.ICAOAddress, FlightID: a.CurrentFlightID,
		MessageID: fmt.Sprintf("%s-FUEL-%d", a.CurrentFlightID, time.Now().Unix()),
		Type:      MsgTypeFuel,
		Timestamp: time.Now(),
	}
	msg, _ := NewHighMediumPriorityMessage(baseMsg, fuelData)
	a.EnqueueMessage(msg)
}

// sendWeatherReport 将报告放入飞机的发件箱
func sendWeatherReport(a *Aircraft) {
	log.Printf("📡 [飞机 %s] 准备发送气象报告...", a.CurrentFlightID)
	type WeatherReportData struct {
		TemperatureC  float64
		WindSpeedKPH  float64
		WindDirection int
		Timestamp     time.Time
	}
	weatherData := WeatherReportData{
		TemperatureC: -50.0, WindSpeedKPH: 120.0, WindDirection: 270, Timestamp: time.Now(),
	}
	baseMsg := ACARSBaseMessage{
		AircraftICAOAddress: a.ICAOAddress, FlightID: a.CurrentFlightID,
		MessageID: fmt.Sprintf("%s-WX-%d", a.CurrentFlightID, time.Now().Unix()),
		Type:      MsgTypeWeather,
		Timestamp: time.Now(),
	}
	msg, _ := NewMediumLowPriorityMessage(baseMsg, weatherData)
	a.EnqueueMessage(msg)
}

// sendPositionReport 将报告放入飞机的发件箱
func sendPositionReport(a *Aircraft) {
	log.Printf("📡 [飞机 %s] 准备发送例行位置报告...", a.CurrentFlightID)
	posData := PositionReportData{Latitude: 39.9, Longitude: 116.3, Altitude: 35000}
	baseMsg := ACARSBaseMessage{
		AircraftICAOAddress: a.ICAOAddress, FlightID: a.CurrentFlightID,
		MessageID: fmt.Sprintf("%s-POS-%d", a.CurrentFlightID, time.Now().Unix()),
		Type:      MsgTypePosition,
		Timestamp: time.Now(),
	}
	msg, _ := NewHighMediumPriorityMessage(baseMsg, posData)
	a.EnqueueMessage(msg)
}

// sendOOOIMessage 将报告放入飞机的发件箱
func sendOOOIMessage(a *Aircraft, oooiType string, eventTime time.Time) {
	log.Printf("📡 [飞机 %s] 准备发送 OOOI 报告: %s", a.CurrentFlightID, oooiType)
	var oooiData OOOIReportData
	switch oooiType {
	case "OUT":
		oooiData.OutTime = eventTime
	case "OFF":
		oooiData.OffTime = eventTime
	case "ON":
		oooiData.OnTime = eventTime
	case "IN":
		oooiData.InTime = eventTime
	}
	baseMsg := ACARSBaseMessage{
		AircraftICAOAddress: a.ICAOAddress, FlightID: a.CurrentFlightID,
		MessageID: fmt.Sprintf("%s-%s-%d", a.CurrentFlightID, oooiType, time.Now().Unix()),
		Type:      MsgTypeOOOI,
		Timestamp: time.Now(),
	}
	msg, _ := NewHighMediumPriorityMessage(baseMsg, oooiData)
	a.EnqueueMessage(msg)
}
