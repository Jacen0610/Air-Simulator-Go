package main

import (
	"log"
	"time"
)

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.Println("--- ACARS 模拟: 包含 ACK 信道争用的双向通信 ---")

	// 1. 创建一个所有参与者共享的通信信道
	vhfChannel := &Channel{}

	// 2. 实例化地面控制中心
	groundStation := NewGroundControlCenter("ZBAA_GND")

	// 3. 实例化两架飞机
	aircraft1 := NewAircraft("B-1234", "B-1234", "A320", "Airbus", "SN1234", "CCA")
	aircraft1.CurrentFlightID = "CCA981"

	aircraft2 := NewAircraft("B-5678", "B-5678", "B777", "Boeing", "SN5678", "CSN")
	aircraft2.CurrentFlightID = "CSN310"

	// --- 模拟场景: 两架飞机几乎同时发送报文，观察地面站 ACK 的发送时机 ---

	// 4. 飞机1 (CCA981) 准备发送位置报告
	posReportData1 := PositionReportData{Latitude: 39.9, Longitude: 116.3}
	baseMsg1 := ACARSBaseMessage{
		AircraftICAOAddress: aircraft1.ICAOAddress,
		FlightID:            aircraft1.CurrentFlightID,
		MessageID:           "CCA981-POS-001",
	}
	posMessage1, _ := NewHighMediumPriorityMessage(baseMsg1, posReportData1)

	// 5. 飞机2 (CSN310) 准备发送发动机报告
	engineData2 := EngineReportData{EngineID: 1, N1RPM: 85.5}
	baseMsg2 := ACARSBaseMessage{
		AircraftICAOAddress: aircraft2.ICAOAddress,
		FlightID:            aircraft2.CurrentFlightID,
		MessageID:           "CSN310-ENG-001",
	}
	engineMessage2, _ := NewMediumLowPriorityMessage(baseMsg2, engineData2)

	// 6. 让两架飞机并发地尝试发送报文
	go aircraft1.SendMessage(posMessage1, vhfChannel, groundStation)
	// 稍微错开一点时间，让竞争更有趣
	time.Sleep(50 * time.Millisecond)
	go aircraft2.SendMessage(engineMessage2, vhfChannel, groundStation)

	// 7. 主程序等待足够长的时间，以观察完整的来回通信
	log.Println("--- 主程序等待5秒以观察完整的双向通信和信道竞争 ---")
	time.Sleep(5 * time.Second)
	log.Println("--- 模拟结束 ---")
}
