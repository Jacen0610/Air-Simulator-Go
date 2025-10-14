// C:/workspace/go/Air-Simulator-Go/simulation/simulation_plan.go
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
	Type             string // "Departing" (离港), "Arriving" (进港), "Cruising" (巡航), "FULL" (完整飞行)
}

// flightPlans 变量 (修改后)
var flightPlans = []FlightPlan{
	// 单架飞机完成一次完整的起降巡航飞行计划
	{Type: "FULL", StartTimeMinutes: 1},
}

var AircraftCount = len(flightPlans)

// **[核心修正]** RunSimulationSession 现在是一个阻塞函数。
// 它会等待所有飞行计划都完成后才返回。
func RunSimulationSession(aircraftList []*Aircraft) {
	// 为飞行计划分配飞机实例
	if len(flightPlans) != len(aircraftList) {
		log.Fatalf("错误: 飞行计划数量 (%d) 与飞机数量 (%d) 不匹配!", len(flightPlans), len(aircraftList))
		return
	}
	for i := range flightPlans {
		flightPlans[i].Aircraft = aircraftList[i]
	}

	// 使用一个局部的 WaitGroup 来管理本次会话中所有飞行计划的生命周期。
	var sessionWg sync.WaitGroup

	log.Println("🛫 开始执行静态飞行计划...")
	// 为每个飞行计划启动一个独立的模拟 goroutine
	for i := range flightPlans {
		sessionWg.Add(1)
		plan := flightPlans[i]
		// 将局部的 WaitGroup 传递给 simulateFlight
		go simulateFlight(plan, &sessionWg)
	}

	// **这是最关键的改动**:
	// 程序会阻塞在这里，直到所有由该函数启动的 simulateFlight goroutine 都调用了 wg.Done()。
	sessionWg.Wait()
}

// simulateFlight 函数现在接收 sessionWg 的指针
func simulateFlight(plan FlightPlan, wg *sync.WaitGroup) {
	defer wg.Done()

	// 1. 等待至预定的飞行计划开始时间
	startTime := time.Duration(plan.StartTimeMinutes) * time.Minute
	time.Sleep(startTime)
	log.Printf("🛫 [飞机 %s] 飞行计划启动。类型: %s, 计划开始于 %d 分钟", plan.Aircraft.CurrentFlightID, plan.Type, plan.StartTimeMinutes)

	// 2. 根据飞行计划类型执行不同的通信逻辑
	if plan.Type == "FULL" {
		// ########### 起飞阶段 ###########
		log.Printf("🛫 [飞机 %s] 开始执行起飞程序...", plan.Aircraft.CurrentFlightID)
		sendOOOIMessage(plan.Aircraft, "OUT", time.Now()) // 推出
		time.Sleep(config.TaxiTime)                       // 滑行
		sendOOOIMessage(plan.Aircraft, "OFF", time.Now()) // 起飞

		// --- 起飞后5分钟，每分钟发送引擎报告 ---
		log.Printf("✈️  [飞机 %s] 进入起飞后初始爬升阶段，将持续报告引擎状况...", plan.Aircraft.CurrentFlightID)
		engineReportTickerTakeoff := time.NewTicker(30 * time.Second)
		engineReportTimerTakeoff := time.NewTimer(5 * time.Minute)
	fullInitialClimbLoop:
		for {
			select {
			case <-engineReportTickerTakeoff.C:
				sendEngineReport(plan.Aircraft)
			case <-engineReportTimerTakeoff.C:
				engineReportTickerTakeoff.Stop()
				time.Sleep(30 * time.Second)
				break fullInitialClimbLoop
			}
		}
		log.Printf("✈️  [飞机 %s] 初始爬升阶段结束，准备进入巡航。", plan.Aircraft.CurrentFlightID)

		// ########### 巡航阶段 ###########
		log.Printf("✈️  [飞机 %s] 进入巡航阶段...", plan.Aircraft.CurrentFlightID)
		sendATKMessage(plan.Aircraft, "MAINTAIN FL350") // 进入巡航时发送指令

		// --- 模拟30分钟的巡航飞行，包含多种报告 ---
		posTickerCruise := time.NewTicker(config.PosReportInterval)
		fuelTickerCruise := time.NewTicker(config.FuelReportInterval)
		weatherTickerCruise := time.NewTicker(config.WeatherReportInterval)
		flightTimerCruise := time.NewTimer(config.FlightDuration)

	fullFlightLoopCruise:
		for {
			select {
			case <-posTickerCruise.C:
				sendPositionReport(plan.Aircraft)
			case <-fuelTickerCruise.C:
				sendFuelReport(plan.Aircraft)
			case <-weatherTickerCruise.C:
				sendWeatherReport(plan.Aircraft)
			case <-flightTimerCruise.C:
				time.Sleep(30 * time.Second)
				break fullFlightLoopCruise
			}
		}
		posTickerCruise.Stop()
		fuelTickerCruise.Stop()
		weatherTickerCruise.Stop()
		flightTimerCruise.Stop()

		sendATKMessage(plan.Aircraft, "DESCEND AND MAINTAIN FL240") // 巡航结束时发送指令
		log.Printf("✈️  [飞机 %s] 巡航结束，准备进入降落程序。", plan.Aircraft.CurrentFlightID)

		// ########### 降落阶段 ###########
		log.Printf("🛬 [飞机 %s] 开始执行降落程序...", plan.Aircraft.CurrentFlightID)
		sendPositionReport(plan.Aircraft) // 进入近场时首先报告位置

		landingReportTicker := time.NewTicker(30 * time.Second)
		landingReportTimer := time.NewTimer(5 * time.Minute)
	fullLandingLoop:
		for {
			select {
			case <-landingReportTicker.C:
				sendPositionReport(plan.Aircraft)
			case <-landingReportTimer.C:
				landingReportTicker.Stop()
				time.Sleep(30 * time.Second)
				break fullLandingLoop
			}
		}

		sendOOOIMessage(plan.Aircraft, "ON", time.Now()) // 降落
		// --- 降落后5分钟，每分钟发送引擎报告 ---
		log.Printf("🛬 [飞机 %s] 完成降落，将持续报告引擎反推及冷却状况...", plan.Aircraft.CurrentFlightID)
		engineReportTickerLanding := time.NewTicker(1 * time.Minute)
		engineReportTimerLanding := time.NewTimer(5 * time.Minute)
	fullLandingRollLoop:
		for {
			select {
			case <-engineReportTickerLanding.C:
				sendEngineReport(plan.Aircraft)
			case <-engineReportTimerLanding.C:
				engineReportTickerLanding.Stop()
				time.Sleep(30 * time.Second)
				break fullLandingRollLoop
			}
		}

		// 滑行至停机位
		sendOOOIMessage(plan.Aircraft, "IN", time.Now()) // 到达
		time.Sleep(30 * time.Second)
		log.Printf("🛬 [飞机 %s] 已成功降落并抵达停机位。完整飞行计划结束。", plan.Aircraft.CurrentFlightID)

	} else if plan.Type == "Departing" {
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
				time.Sleep(30 * time.Second)
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
				time.Sleep(30 * time.Second)
				break flightLoopDepart
			}
		}

		log.Printf("✈️  [飞机 %s] 已飞出空域。飞行计划结束。", plan.Aircraft.CurrentFlightID)

	} else if plan.Type == "Arriving" { // Arriving
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
				time.Sleep(30 * time.Second)
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
				time.Sleep(30 * time.Second)
				break landingRollLoop
			}
		}

		time.Sleep(config.TaxiTime)                  // 滑行至停机位
		sendOOOIMessage(plan.Aircraft, "IN", onTime) // 到达
		time.Sleep(30 * time.Second)
		log.Printf("🛬 [飞机 %s] 已成功降落并抵达停机位。飞行计划结束。", plan.Aircraft.CurrentFlightID)
	} else if plan.Type == "Cruising" {
		// 巡航飞机流程
		sendATKMessage(plan.Aircraft, "MAINTAIN FL350") // 进入巡航时发送指令

		// --- 模拟30分钟的巡航飞行，包含多种报告 ---
		posTicker := time.NewTicker(config.PosReportInterval)
		defer posTicker.Stop()
		fuelTicker := time.NewTicker(config.FuelReportInterval)
		defer fuelTicker.Stop()
		weatherTicker := time.NewTicker(config.WeatherReportInterval)
		defer weatherTicker.Stop()
		flightTimer := time.NewTimer(config.FlightDuration)
		defer flightTimer.Stop()

	flightLoopCruise:
		for {
			select {
			case <-posTicker.C:
				sendPositionReport(plan.Aircraft)
			case <-fuelTicker.C:
				sendFuelReport(plan.Aircraft)
			case <-weatherTicker.C:
				sendWeatherReport(plan.Aircraft)
			case <-flightTimer.C:
				time.Sleep(30 * time.Second)
				break flightLoopCruise
			}
		}
		sendATKMessage(plan.Aircraft, "DESCEND AND MAINTAIN FL240") // 巡航结束时发送指令
		log.Printf("✈️  [飞机 %s] 巡航结束。飞行计划结束。", plan.Aircraft.CurrentFlightID)
	}
}

// sendATKMessage 发送空中管制指令
func sendATKMessage(a *Aircraft, content string) {
	log.Printf("📡 [飞机 %s] 准备发送 ATK 指令: %s", a.CurrentFlightID, content)
	atkData := ATCMessageData{
		ATCMsgType: "INSTRUCTION",
		Content:    content,
	}
	baseMsg := ACARSBaseMessage{
		AircraftICAOAddress: a.ICAOAddress,
		FlightID:            a.CurrentFlightID,
		MessageID:           fmt.Sprintf("%s-ATK-%d", a.CurrentFlightID, time.Now().UnixNano()),
		Type:                MsgTypeATK,
		Timestamp:           time.Now(),
	}
	msg, _ := NewCriticalPriorityMessage(baseMsg, atkData)
	a.EnqueueMessage(msg)
}

// ... 其余 send... 函数保持不变 ...
func sendEngineReport(a *Aircraft) { // 不再需要 commsSystem
	log.Printf("📡 [飞机 %s] 生成引擎报告并放入队列...", a.CurrentFlightID)
	engineData := EngineReportData{
		EngineID: 1, N1RPM: 85.5, EGT: 450, FuelFlow: 1200, OilPressure: 75,
		FlightPhase: "CLIMB", ReportTimeUTC: time.Now().UTC(),
	}
	baseMsg := ACARSBaseMessage{
		AircraftICAOAddress: a.ICAOAddress, FlightID: a.CurrentFlightID,
		MessageID: fmt.Sprintf("%s-ENG-%d", a.CurrentFlightID, time.Now().UnixNano()), // 使用纳秒确保唯一性
		Type:      MsgTypeEngineReport,
		Timestamp: time.Now(),
	}
	msg, _ := NewMediumPriorityMessage(baseMsg, engineData)
	a.EnqueueMessage(msg) // 调用新的入队方法
}

// sendFuelReport 更新为接收 CommunicationSystem
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
	msg, _ := NewHighPriorityMessage(baseMsg, fuelData)
	a.EnqueueMessage(msg)
}

// sendWeatherReport 更新为接收 CommunicationSystem
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
	msg, _ := NewMediumPriorityMessage(baseMsg, weatherData)
	a.EnqueueMessage(msg)
}

// sendPositionReport 更新为接收 CommunicationSystem
func sendPositionReport(a *Aircraft) {
	log.Printf("📡 [飞机 %s] 准备发送例行位置报告...", a.CurrentFlightID)
	posData := PositionReportData{Latitude: 39.9, Longitude: 116.3, Altitude: 35000}
	baseMsg := ACARSBaseMessage{
		AircraftICAOAddress: a.ICAOAddress, FlightID: a.CurrentFlightID,
		MessageID: fmt.Sprintf("%s-POS-%d", a.CurrentFlightID, time.Now().Unix()),
		Type:      MsgTypePosition,
		Timestamp: time.Now(),
	}
	msg, _ := NewHighPriorityMessage(baseMsg, posData)
	a.EnqueueMessage(msg)
}

// sendOOOIMessage 更新为接收 CommunicationSystem
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
	msg, _ := NewHighPriorityMessage(baseMsg, oooiData)
	a.EnqueueMessage(msg)
}
