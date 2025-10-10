package simulation

import (
	"Air-Simulator/config"
	"fmt"
	"log"
	"sync"
	"time"
)

// RunFlightPlan 模拟一个从起飞到降落的完整30分钟飞行计划
func RunFlightPlan(aircraft *Aircraft, stopCh chan struct{}) {
	log.Println("✈️  飞行计划已启动，总时长30分钟。")

	var flightWg sync.WaitGroup

	time.AfterFunc(1*time.Minute, func() { sendOooiMessage(aircraft, "OUT", time.Now()) })
	time.AfterFunc(3*time.Minute, func() { sendOooiMessage(aircraft, "OFF", time.Now()) })
	time.AfterFunc(28*time.Minute, func() { sendOooiMessage(aircraft, "ON", time.Now()) })
	time.AfterFunc(30*time.Minute, func() { sendOooiMessage(aircraft, "IN", time.Now()) })

	// T+3m 到 T+8m: 初始爬升阶段 (5分钟)
	flightWg.Add(1)
	go func() {
		defer flightWg.Done()
		<-time.After(3 * time.Minute)
		log.Println("✈️  [T+3m] 进入初始爬升阶段 (5分钟)，高频报告引擎状态...")
		engineTicker := time.NewTicker(1 * time.Minute)
		defer engineTicker.Stop()
		phaseTimer := time.NewTimer(5 * time.Minute)
		defer phaseTimer.Stop()
		for {
			select {
			case <-engineTicker.C:
				sendEngineReport(aircraft)
			case <-phaseTimer.C:
				return
			case <-stopCh:
				return
			}
		}
	}()

	// T+8m 到 T+25m: 巡航阶段 (17分钟)
	flightWg.Add(1)
	go func() {
		defer flightWg.Done()
		<-time.After(8 * time.Minute)
		log.Println("✈️  [T+8m] 进入巡航阶段 (17分钟)，常规报告状态...")
		posTicker := time.NewTicker(config.PosReportInterval)
		defer posTicker.Stop()
		fuelTicker := time.NewTicker(config.FuelReportInterval)
		defer fuelTicker.Stop()
		weatherTicker := time.NewTicker(config.WeatherReportInterval)
		defer weatherTicker.Stop()
		phaseTimer := time.NewTimer(17 * time.Minute)
		defer phaseTimer.Stop()
		for {
			select {
			case <-posTicker.C:
				sendPositionReport(aircraft)
			case <-fuelTicker.C:
				sendFuelReport(aircraft)
			case <-weatherTicker.C:
				sendWeatherReport(aircraft)
			case <-phaseTimer.C:
				return
			case <-stopCh:
				return
			}
		}
	}()

	// T+25m 到 T+28m: 降落阶段 (3分钟)
	flightWg.Add(1)
	go func() {
		defer flightWg.Done()
		<-time.After(25 * time.Minute)
		log.Println("✈️  [T+25m] 进入降落阶段 (3分钟)，高频报告位置...")
		landingPosTicker := time.NewTicker(30 * time.Second)
		defer landingPosTicker.Stop()
		phaseTimer := time.NewTimer(3 * time.Minute)
		defer phaseTimer.Stop()
		for {
			select {
			case <-landingPosTicker.C:
				sendPositionReport(aircraft) // 进近时高频发送位置报告
			case <-phaseTimer.C:
				return
			case <-stopCh:
				return
			}
		}
	}()

	// 等待外部的停止信号
	<-stopCh
	flightWg.Wait()
	log.Println("✈️  飞行计划模拟被外部信号终止。")
}

// --- 报文发送辅助函数 ---

func sendOooiMessage(a *Aircraft, oooiType string, eventTime time.Time) {
	log.Printf("📡 [飞机 %s] 发送 OOOI 报文: %s", a.CurrentFlightID, oooiType)
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
	baseMsg := ACARSBaseMessage{AircraftICAOAddress: a.ICAOAddress, FlightID: a.CurrentFlightID, MessageID: fmt.Sprintf("%s-%s-%d", a.CurrentFlightID, oooiType, time.Now().UnixNano()), Type: MsgTypeOOOI, Timestamp: time.Now()}
	msg, _ := NewHighMediumPriorityMessage(baseMsg, oooiData)
	a.EnqueueMessage(msg)
}

func sendEngineReport(a *Aircraft) {
	log.Printf("📡 [飞机 %s] 生成引擎报告...", a.CurrentFlightID)
	engineData := EngineReportData{EngineID: 1, N1RPM: 85.5, EGT: 450, FuelFlow: 1200, OilPressure: 75, FlightPhase: "CLIMB", ReportTimeUTC: time.Now().UTC()}
	baseMsg := ACARSBaseMessage{AircraftICAOAddress: a.ICAOAddress, FlightID: a.CurrentFlightID, MessageID: fmt.Sprintf("%s-ENG-%d", a.CurrentFlightID, time.Now().UnixNano()), Type: MsgTypeEngineReport, Timestamp: time.Now()}
	msg, _ := NewMediumLowPriorityMessage(baseMsg, engineData)
	a.EnqueueMessage(msg)
}

func sendPositionReport(a *Aircraft) {
	log.Printf("📡 [飞机 %s] 生成位置报告...", a.CurrentFlightID)
	posData := PositionReportData{Latitude: 34.05, Longitude: -118.24, Altitude: 35000}
	baseMsg := ACARSBaseMessage{AircraftICAOAddress: a.ICAOAddress, FlightID: a.CurrentFlightID, MessageID: fmt.Sprintf("%s-POS-%d", a.CurrentFlightID, time.Now().UnixNano()), Type: MsgTypePosition, Timestamp: time.Now()}
	msg, _ := NewHighMediumPriorityMessage(baseMsg, posData)
	a.EnqueueMessage(msg)
}

func sendFuelReport(a *Aircraft) {
	log.Printf("📡 [飞机 %s] 生成燃油报告...", a.CurrentFlightID)
	fuelData := FuelReportData{RemainingFuelKG: 10000.0, FuelFlowKGPH: 2200.0}
	baseMsg := ACARSBaseMessage{AircraftICAOAddress: a.ICAOAddress, FlightID: a.CurrentFlightID, MessageID: fmt.Sprintf("%s-FUEL-%d", a.CurrentFlightID, time.Now().UnixNano()), Type: MsgTypeFuel, Timestamp: time.Now()}
	msg, _ := NewHighMediumPriorityMessage(baseMsg, fuelData)
	a.EnqueueMessage(msg)
}

func sendWeatherReport(a *Aircraft) {
	log.Printf("📡 [飞机 %s] 生成气象报告...", a.CurrentFlightID)
	weatherData := struct{ TemperatureC float64 }{TemperatureC: -55.0}
	baseMsg := ACARSBaseMessage{AircraftICAOAddress: a.ICAOAddress, FlightID: a.CurrentFlightID, MessageID: fmt.Sprintf("%s-WX-%d", a.CurrentFlightID, time.Now().UnixNano()), Type: MsgTypeWeather, Timestamp: time.Now()}
	msg, _ := NewMediumLowPriorityMessage(baseMsg, weatherData)
	a.EnqueueMessage(msg)
}
