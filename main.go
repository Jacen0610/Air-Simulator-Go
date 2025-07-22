// C:/workspace/go/Air-Simulator-Go/main.go
package main

import (
	"Air-Simulator/collector"  // 导入新的 collector 包
	"Air-Simulator/simulation" // 导入新的 simulation 包
	"fmt"
	"log"
	"sync"
	"time"
)

func main() {
	log.Println("=============================================")
	log.Println("======  Air-Ground Communication Simulation  ======")
	log.Println("=============================================")

	// --- 1. 创建通信信道 ---
	initialPMap := simulation.PriorityPMap{
		simulation.CriticalPriority: 0.9,
		simulation.HighPriority:     0.7,
		simulation.MediumPriority:   0.4,
		simulation.LowPriority:      0.2,
	}
	commsChannel := simulation.NewChannel(initialPMap)
	go commsChannel.StartDispatching()
	log.Printf("📡 通信信道已创建并启动，时隙: %v", simulation.TimeSlot)

	// --- 2. 创建地面控制中心 ---
	groundControl := simulation.NewGroundControlCenter("GND_CTL_SEU")
	go groundControl.StartListening(commsChannel, simulation.TimeSlot)

	// --- 3. 创建20架飞机 ---
	aircraftList := make([]*simulation.Aircraft, 20)
	for i := 0; i < 20; i++ {
		icao := fmt.Sprintf("A%d", 70000+i)
		reg := fmt.Sprintf("B-%d", 6000+i)
		flightID := fmt.Sprintf("CES-%d", 1001+i)
		aircraft := simulation.NewAircraft(icao, reg, "A320neo", "Airbus", "MSN1234"+fmt.Sprintf("%d", i), "CES")
		aircraft.CurrentFlightID = flightID
		aircraftList[i] = aircraft
		go aircraft.StartListening(commsChannel)
	}
	log.Printf("✈️  已成功创建 %d 架飞机.", len(aircraftList))

	// --- 4. 启动数据收集器 ---
	var collectorWg sync.WaitGroup
	collectorWg.Add(1)
	doneChan := make(chan struct{})

	dataCollector := collector.NewDataCollector(&collectorWg, doneChan, aircraftList, groundControl, commsChannel)
	go dataCollector.Run()

	// --- 5. 运行飞行计划模拟 ---
	log.Println("🛫 开始执行所有飞行计划...")
	var simWg sync.WaitGroup
	simulation.RunSimulationSession(&simWg, commsChannel, aircraftList)

	// 等待所有飞行计划完成
	simWg.Wait()
	log.Println("✅ 所有飞行计划已执行完毕.")
	time.Sleep(5 * time.Minute)
	// --- 6. 停止收集器并等待文件保存 ---
	log.Println("... 正在停止数据收集器并保存结果 ...")
	close(doneChan)    // 发送信号，通知收集器停止并保存
	collectorWg.Wait() // 等待收集器完成最后的保存工作

	log.Println("Simulation finished.")
}
