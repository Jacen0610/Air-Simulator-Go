package main

import (
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"
)

// --- 模拟参数 ---
const (
	numAircraft        = 5                // 要模拟的飞机数量
	simulationDuration = 10 * time.Second // 模拟运行的总时长
	timeSlot           = 160 * time.Millisecond
)

// aircraftBehaviorLoop 定义了单架飞机的行为逻辑：在模拟期间随机发送报文。
func aircraftBehaviorLoop(a *Aircraft, wg *sync.WaitGroup, commsChannel *Channel, pMap PriorityPMap, stopChan <-chan struct{}) {
	defer wg.Done() // 当函数退出时，通知 WaitGroup

	// 创建一个随机数生成器，避免所有 goroutine 在同一时间发送
	localRand := rand.New(rand.NewSource(time.Now().UnixNano()))

	for {
		select {
		case <-stopChan:
			// 收到停止信号，退出循环
			log.Printf("🛬 [飞机 %s] 行为循环停止。", a.CurrentFlightID)
			return
		default:
			// 1. 等待一个随机的时间间隔 (1 到 5 秒)
			randomDelay := time.Duration(1000+localRand.Intn(4000)) * time.Millisecond
			time.Sleep(randomDelay)

			// 2. 随机创建一个报文并发送
			createAndSendMessage(a, commsChannel, pMap)
		}
	}
}

// createAndSendMessage 随机决定要发送的报文类型，创建并启动发送流程。
func createAndSendMessage(a *Aircraft, commsChannel *Channel, pMap PriorityPMap) {
	// 随机决定报文的优先级
	// 5% 概率为关键报文, 35% 概率为高优先级, 60% 概率为中等优先级
	msgTypeRoll := rand.Intn(100)
	var msg ACARSMessageInterface
	var err error
	msgID := fmt.Sprintf("%s-%d", a.CurrentFlightID, time.Now().UnixNano())

	if msgTypeRoll < 5 {
		// 创建一个关键优先级的故障报告
		faultData := AircraftFaultData{FaultCode: "ENG-FAIL", Description: "Engine 1 Failure", Severity: "CRITICAL"}
		baseMsg := ACARSBaseMessage{
			AircraftICAOAddress: a.ICAOAddress,
			FlightID:            a.CurrentFlightID,
			MessageID:           msgID,
			Type:                MsgTypeAircraftFault,
		}
		msg, err = NewCriticalHighPriorityMessage(baseMsg, faultData)

	} else if msgTypeRoll < 40 {
		// 创建一个高优先级的燃油报告
		fuelData := FuelReportData{RemainingFuelKG: 12000.5, FuelFlowKGPH: 2500.0}
		baseMsg := ACARSBaseMessage{
			AircraftICAOAddress: a.ICAOAddress,
			FlightID:            a.CurrentFlightID,
			MessageID:           msgID,
			Type:                MsgTypeFuel,
		}
		msg, err = NewHighMediumPriorityMessage(baseMsg, fuelData)

	} else {
		// 创建一个中等优先级的发动机报告
		engineData := EngineReportData{EngineID: 1, N1RPM: 88.2, EGT: 550.0}
		baseMsg := ACARSBaseMessage{
			AircraftICAOAddress: a.ICAOAddress,
			FlightID:            a.CurrentFlightID,
			MessageID:           msgID,
			Type:                MsgTypeEngineReport,
		}
		msg, err = NewMediumLowPriorityMessage(baseMsg, engineData)
	}

	if err != nil {
		log.Printf("错误: [%s] 创建报文失败: %v", a.CurrentFlightID, err)
		return
	}

	// 在一个新的 goroutine 中发送报文，这样飞机的行为循环就不会被阻塞
	go a.SendMessage(msg, commsChannel, pMap, timeSlot)
}

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.Println("--- ACARS 模拟: 多飞机随机通信压力测试 ---")

	// 1. 定义不同优先级的发送概率 p
	priorityPValues := PriorityPMap{
		CriticalPriority: 0.9, // 关键报文有 90% 的概率立即发送
		HighPriority:     0.7, // 高优先级报文有 75% 的概率
		MediumPriority:   0.5, // 中等优先级报文有 60% 的概率
		LowPriority:      0.2, // 低优先级报文只有 40% 的概率
	}

	// 2. 初始化核心组件
	vhfChannel := NewChannel()
	go vhfChannel.StartDispatching()

	groundStation := NewGroundControlCenter("ZBAA_GND")
	go groundStation.StartListening(vhfChannel, priorityPValues, timeSlot)

	// 3. 创建并启动多架飞机
	var wg sync.WaitGroup
	stopChan := make(chan struct{}) // 用于通知所有 goroutine 停止
	aircraftList := make([]*Aircraft, numAircraft)

	for i := 0; i < numAircraft; i++ {
		flightID := fmt.Sprintf("CCA%d", 800+i)
		icao := fmt.Sprintf("B-C%03d", i)
		aircraft := NewAircraft(icao, icao, "A320", "Airbus", "SN-C"+icao, "CCA")
		aircraft.CurrentFlightID = flightID
		aircraftList[i] = aircraft

		// 启动飞机的后台 ACK 监听器
		go aircraft.StartListening(vhfChannel)

		// 启动飞机的行为循环
		wg.Add(1)
		go aircraftBehaviorLoop(aircraft, &wg, vhfChannel, priorityPValues, stopChan)
	}

	// 4. 运行模拟
	log.Printf("--- 模拟开始，将运行 %v ---", simulationDuration)
	time.Sleep(simulationDuration)

	// 5. 停止模拟并收集结果
	log.Println("--- 模拟时间到，正在停止所有飞机行为并收集统计数据... ---")
	close(stopChan) // 发送停止信号
	wg.Wait()       // 等待所有飞机的行为循环优雅地退出

	fmt.Println("\n==================== 最终通信统计报告 ====================")
	for _, ac := range aircraftList {
		fmt.Print(ac.GetCommunicationStats())
	}
	fmt.Println("==========================================================")
	log.Println("--- 模拟结束 ---")
}
