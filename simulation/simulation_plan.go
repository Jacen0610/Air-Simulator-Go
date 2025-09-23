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
	Type             string // "Departing" (离港) 或 "Arriving" (进港)
}

// flightPlans 变量 (无变化)
var flightPlans = []FlightPlan{
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

	/*// 20架飞机的飞行计划
	{Type: "Departing", StartTimeMinutes: 1},
	{Type: "Departing", StartTimeMinutes: 2},
	{Type: "Departing", StartTimeMinutes: 3},
	{Type: "Departing", StartTimeMinutes: 4},
	{Type: "Departing", StartTimeMinutes: 5},
	{Type: "Departing", StartTimeMinutes: 6},
	{Type: "Departing", StartTimeMinutes: 7},
	{Type: "Departing", StartTimeMinutes: 8},
	{Type: "Departing", StartTimeMinutes: 9},
	{Type: "Departing", StartTimeMinutes: 10},
	{Type: "Departing", StartTimeMinutes: 11},
	{Type: "Departing", StartTimeMinutes: 12},
	{Type: "Departing", StartTimeMinutes: 13},
	{Type: "Departing", StartTimeMinutes: 14},
	{Type: "Departing", StartTimeMinutes: 15},
	{Type: "Departing", StartTimeMinutes: 16},
	{Type: "Departing", StartTimeMinutes: 17},
	{Type: "Departing", StartTimeMinutes: 18},
	{Type: "Departing", StartTimeMinutes: 19},
	{Type: "Departing", StartTimeMinutes: 20},
	{Type: "Departing", StartTimeMinutes: 21},
	{Type: "Departing", StartTimeMinutes: 22},
	{Type: "Departing", StartTimeMinutes: 23},
	{Type: "Departing", StartTimeMinutes: 24},
	{Type: "Departing", StartTimeMinutes: 25},
	{Type: "Departing", StartTimeMinutes: 26},
	{Type: "Departing", StartTimeMinutes: 27},
	{Type: "Departing", StartTimeMinutes: 28},
	{Type: "Departing", StartTimeMinutes: 29},
	{Type: "Departing", StartTimeMinutes: 30},

	{Type: "Arriving", StartTimeMinutes: 1},
	{Type: "Arriving", StartTimeMinutes: 2},
	{Type: "Arriving", StartTimeMinutes: 3},
	{Type: "Arriving", StartTimeMinutes: 4},
	{Type: "Arriving", StartTimeMinutes: 5},
	{Type: "Arriving", StartTimeMinutes: 6},
	{Type: "Arriving", StartTimeMinutes: 7},
	{Type: "Arriving", StartTimeMinutes: 8},
	{Type: "Arriving", StartTimeMinutes: 9},
	{Type: "Arriving", StartTimeMinutes: 10},
	{Type: "Arriving", StartTimeMinutes: 11},
	{Type: "Arriving", StartTimeMinutes: 12},
	{Type: "Arriving", StartTimeMinutes: 13},
	{Type: "Arriving", StartTimeMinutes: 14},
	{Type: "Arriving", StartTimeMinutes: 15},
	{Type: "Arriving", StartTimeMinutes: 16},
	{Type: "Arriving", StartTimeMinutes: 17},
	{Type: "Arriving", StartTimeMinutes: 18},
	{Type: "Arriving", StartTimeMinutes: 19},
	{Type: "Arriving", StartTimeMinutes: 20},
	{Type: "Arriving", StartTimeMinutes: 21},
	{Type: "Arriving", StartTimeMinutes: 22},
	{Type: "Arriving", StartTimeMinutes: 23},
	{Type: "Arriving", StartTimeMinutes: 24},
	{Type: "Arriving", StartTimeMinutes: 25},
	{Type: "Arriving", StartTimeMinutes: 26},
	{Type: "Arriving", StartTimeMinutes: 27},
	{Type: "Arriving", StartTimeMinutes: 28},
	{Type: "Arriving", StartTimeMinutes: 29},
	{Type: "Arriving", StartTimeMinutes: 30},*/
}
var AircraftCount = len(flightPlans)

// RunSimulationSession 更新为接收 CommunicationSystem
func RunSimulationSession(wg *sync.WaitGroup, commsSystem *CommunicationSystem, aircraftList []*Aircraft) {
	// 为飞行计划分配飞机实例
	for i := range flightPlans {
		flightPlans[i].Aircraft = aircraftList[i]
	}

	// 为每个飞行计划启动一个独立的模拟 goroutine
	for i := range flightPlans {
		wg.Add(1)
		plan := flightPlans[i]
		// 传递 commsSystem
		go simulateFlight(plan, wg)
	}
}

// simulateFlight 不再需要 commsSystem 作为参数
func simulateFlight(plan FlightPlan, wg *sync.WaitGroup) {
	defer wg.Done()

	// 1. 等待至预定的飞行计划开始时间
	startTime := time.Duration(plan.StartTimeMinutes) * time.Minute
	time.Sleep(startTime)
	log.Printf("🛫 [飞机 %s] 飞行计划启动。类型: %s, 计划开始于 %d 分钟", plan.Aircraft.CurrentFlightID, plan.Type, plan.StartTimeMinutes)

	// 2. 根据飞行计划类型执行不同的通信逻辑
	if plan.Type == "Departing" {
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
		log.Printf("✈️  [飞机 %s] 初始爬升阶段结束，进入巡航。", plan.Aircraft.CurrentFlightID)

		// --- 模拟30分钟的离港飞行，包含多种报告 ---
		posTicker := time.NewTicker(config.PosReportInterval)
		defer posTicker.Stop()
		fuelTicker := time.NewTicker(config.FuelReportInterval)
		defer fuelTicker.Stop()
		weatherTicker := time.NewTicker(config.WeatherReportInterval)
		defer weatherTicker.Stop()
		flightTimer := time.NewTimer(config.FlightDuration)
		defer flightTimer.Stop()

	flightLoopDepart:
		for {
			select {
			case <-posTicker.C:
				sendPositionReport(plan.Aircraft)
			case <-fuelTicker.C:
				sendFuelReport(plan.Aircraft)
			case <-weatherTicker.C:
				sendWeatherReport(plan.Aircraft)
			case <-flightTimer.C:
				break flightLoopDepart
			}
		}

		log.Printf("✈️  [飞机 %s] 已飞出空域。飞行计划结束。", plan.Aircraft.CurrentFlightID)

	} else { // Arriving
		// 进港飞机流程
		sendPositionReport(plan.Aircraft) // 进入空域时首先报告位置

		// --- 模拟30分钟的进港飞行，包含多种报告 ---
		posTicker := time.NewTicker(config.PosReportInterval)
		defer posTicker.Stop()
		fuelTicker := time.NewTicker(config.FuelReportInterval)
		defer fuelTicker.Stop()
		weatherTicker := time.NewTicker(config.WeatherReportInterval)
		defer weatherTicker.Stop()
		flightTimer := time.NewTimer(config.FlightDuration)
		defer flightTimer.Stop()

	flightLoopArrive:
		for {
			select {
			case <-posTicker.C:
				sendPositionReport(plan.Aircraft)
			case <-fuelTicker.C:
				sendFuelReport(plan.Aircraft)
			case <-weatherTicker.C:
				sendWeatherReport(plan.Aircraft)
			case <-flightTimer.C:
				break flightLoopArrive
			}
		}

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
	}
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
