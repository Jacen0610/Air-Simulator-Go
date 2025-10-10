package main

import (
	"Air-Simulator/collector"
	"Air-Simulator/config"
	"Air-Simulator/simulation"
	"log"
	"sync"
	"time"
)

func main() {
	log.Println("=============================================")
	log.Println("======  Air-Ground Communication Simulation  ======")
	log.Println("====  (Single-Agent Dynamic Environment)   ====")
	log.Println("=============================================")

	runSingleAgentScenario()
}

// runSingleAgentScenario 启动单智能体在动态环境中的强化学习场景
func runSingleAgentScenario() {
	log.Println("🎬 正在启动场景: 单智能体动态环境")

	// --- 1. 环境设置 ---
	channel := simulation.NewChannel("Primary_SA", config.PrimaryTimeSlot)
	commsSystem := simulation.NewCommunicationSystem(channel, nil, nil)
	commsSystem.StartDispatching()

	// --- 2. 创建智能体和环境组件 ---
	var simWg sync.WaitGroup

	metricsChan := make(chan time.Duration, 4096)

	// 创建飞机智能体 (Agent)
	aircraft := simulation.NewAircraft("RL-AGENT-01", "N-RL01", "F-22", "Lockheed", "RL-001", "USAF", metricsChan)
	aircraft.CurrentFlightID = "RL-FLIGHT-1"
	go aircraft.StartListening(commsSystem)

	// [新增] 创建地面站
	groundControl := simulation.NewGroundControlCenter("GND_CTL_SA", metricsChan)
	go groundControl.StartListening(commsSystem)

	// 创建并启动背景流量生成器
	trafficGen := simulation.NewBackgroundTrafficGenerator("BG-Traffic-1", channel, &simWg)
	simWg.Add(1)
	go trafficGen.Start()

	// --- 3. 启动数据收集器 ---
	var collectorWg sync.WaitGroup
	collectorWg.Add(1)
	doneChan := make(chan struct{})

	dataCollector := collector.NewDataCollector(
		&collectorWg,
		doneChan,
		[]*simulation.Aircraft{aircraft},
		[]*simulation.Channel{channel},
		[]*simulation.GroundControlCenter{groundControl}, // [修改] 将地面站加入监控
		metricsChan,
	)
	go dataCollector.Run()

	// --- 4. 执行飞行计划 ---
	flightPlanStopCh := make(chan struct{})
	go simulation.RunFlightPlan(aircraft, flightPlanStopCh)

	// --- 5. 运行并等待模拟结束 ---
	simulationDuration := 31 * time.Minute
	log.Printf("模拟将运行 %v。请观察控制台输出...", simulationDuration)
	<-time.After(simulationDuration)

	// --- 6. 优雅地关闭所有组件 ---
	log.Println("模拟时间已到，正在关闭所有组件...")
	close(flightPlanStopCh)
	time.Sleep(1 * time.Second)
	aircraft.Stop()
	trafficGen.Stop()
	simWg.Wait()

	log.Println("... 正在关闭指标通道，等待数据收集完成 ...")
	close(metricsChan)

	log.Println("... 正在停止数据收集器并保存结果 ...")
	close(doneChan)
	collectorWg.Wait()

	log.Println("=============================================")
	log.Println("========  SINGLE-AGENT RUN FINISHED  ========")
	log.Println("=============================================")
}
