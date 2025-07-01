// C:/workspace/go/Air-Simulator-Go/main.go
package main

import (
	"log"
	"time"
)

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.Println("--- ACARS 模拟: 发送方驱动的 ACK/重传机制 ---")

	// 1. 定义 p-坚持算法的参数
	priorityPValues := PriorityPMap{
		CriticalPriority: 0.8,
		HighPriority:     0.5,
		MediumPriority:   0.2,
		LowPriority:      0.1,
	}
	const timeSlot = 50 * time.Millisecond

	// 2. 创建通信信道并启动其调度服务
	vhfChannel := NewChannel()
	go vhfChannel.StartDispatching()

	// 3. 实例化地面站并启动其监听服务
	groundStation := NewGroundControlCenter("ZBAA_GND")
	// 将信道参数传递给地面站的监听器
	go groundStation.StartListening(vhfChannel, priorityPValues, timeSlot)

	// 4. 实例化飞机并启动它们的监听服务
	aircraft1 := NewAircraft("B-1234", "B-1234", "A320", "Airbus", "SN1234", "CCA")
	aircraft1.CurrentFlightID = "CCA981"
	go aircraft1.StartListening(vhfChannel) // **新步骤：启动飞机的监听器**

	// --- 模拟场景: 飞机发送报文并等待 ACK，如果 GCS 繁忙可能会超时 ---

	// 5. 准备报文
	posReportData := PositionReportData{Latitude: 39.9, Longitude: 116.3}
	baseMsg := ACARSBaseMessage{
		AircraftICAOAddress: aircraft1.ICAOAddress,
		FlightID:            aircraft1.CurrentFlightID,
		MessageID:           "CCA981-POS-001",
		Type:                MsgTypePosition,
	}
	posMessage, _ := NewHighMediumPriorityMessage(baseMsg, posReportData)

	// 6. 让飞机发送报文
	// 注意：SendMessage 现在需要信道参数
	go aircraft1.SendMessage(posMessage, vhfChannel, priorityPValues, timeSlot)

	// 7. 主程序等待足够长的时间以观察完整的交互，包括可能的重传
	log.Println("--- 主程序等待10秒以观察 ACK/超时/重传 ---")
	time.Sleep(10 * time.Second)
	log.Println("--- 模拟结束 ---")
}
