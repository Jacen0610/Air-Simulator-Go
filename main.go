// C:/workspace/go/Air-Simulator-Go/main.go
package main

import (
	"Air-Simulator/collector"
	"Air-Simulator/config" // 导入新的 config 包
	"Air-Simulator/simulation"
	"fmt"
	"log"
	"sync"
	"time"
)

func main() {
	log.Println("=============================================")
	log.Println("======  Air-Ground Communication Simulation  ======")
	log.Println("=============================================")
	if config.EnableBackupChannel {
		log.Printf("加载配置: 双信道模式, 主信道时隙: %v, 备用信道时隙: %v", config.PrimaryTimeSlot, config.BackupTimeSlot)
		log.Printf("加载配置: 主信道PMAP -> %v, 备用信道PMAP -> %v", config.PrimaryPMap, config.BackupPMap)
		log.Printf("加载配置: 切换概率 -> %v", config.SwitchoverProbs)
	} else {
		log.Printf("加载配置: 单信道模式, 主信道时隙: %v", config.PrimaryTimeSlot)
		log.Printf("加载配置: 主信道PMAP -> %v", config.PrimaryPMap)
	}

	log.Println("=============================================")

	// --- 1. 创建信道和通信系统 (所有参数均从 config 包加载) ---
	primaryChannel := simulation.NewChannel("Primary", config.PrimaryPMap, config.PrimaryTimeSlot)
	var backupChannel *simulation.Channel
	if config.EnableBackupChannel {
		backupChannel = simulation.NewChannel("Backup", config.BackupPMap, config.BackupTimeSlot)
	}

	commsSystem := simulation.NewCommunicationSystem(primaryChannel, backupChannel, config.SwitchoverProbs)
	commsSystem.StartDispatching() // 启动所有信道的调度器

	// --- 2. 创建地面站和飞机 ---
	groundControl := simulation.NewGroundControlCenter("GND_CTL_MAIN")
	go groundControl.StartListening(commsSystem)

	aircraftList := make([]*simulation.Aircraft, simulation.AircraftCount)
	for i := 0; i < simulation.AircraftCount; i++ {
		icao := fmt.Sprintf("A%d", 70000+i)
		flightID := fmt.Sprintf("CES%d", 1001+i)
		aircraft := simulation.NewAircraft(icao, fmt.Sprintf("B-%d", 6000+i), "A320neo", "Airbus", "MSN1234"+fmt.Sprintf("%d", i), "CES")
		aircraft.CurrentFlightID = flightID
		aircraftList[i] = aircraft
		go aircraft.StartListening(commsSystem)
	}
	log.Printf("✈️  已成功创建 %d 架飞机.", len(aircraftList))

	// --- 3. 启动独立的数据收集器 ---
	channelsToMonitor := []*simulation.Channel{primaryChannel, backupChannel}
	groundStationsToMonitor := []*simulation.GroundControlCenter{groundControl}

	var collectorWg sync.WaitGroup
	collectorWg.Add(1)
	doneChan := make(chan struct{})

	dataCollector := collector.NewDataCollector(
		&collectorWg,
		doneChan,
		aircraftList,
		channelsToMonitor,
		groundStationsToMonitor,
	)
	go dataCollector.Run()

	// --- 4. 运行飞行计划模拟 ---
	log.Println("🛫 开始执行所有飞行计划...")
	var simWg sync.WaitGroup
	simulation.RunSimulationSession(&simWg, commsSystem, aircraftList)

	// 等待所有飞行计划完成
	simWg.Wait()
	log.Println("✅ 所有飞行计划已执行完毕.")

	// --- 5. 结束并保存 ---
	log.Println("... 等待 1 分钟以确保所有最终的通信完成 ...")
	time.Sleep(1 * time.Minute)

	log.Println("... 正在停止数据收集器并保存结果 ...")
	close(doneChan)    // 发送停止信号
	collectorWg.Wait() // 等待收集器完成文件保存

	log.Println("=============================================")
	log.Println("===========  SIMULATION FINISHED  ===========")
	log.Println("=============================================")
}
