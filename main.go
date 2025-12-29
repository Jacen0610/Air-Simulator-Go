// C:/workspace/go/Air-Simulator-Go/main.go
package main

import (
	"Air-Simulator/api"
	"Air-Simulator/collector"
	"Air-Simulator/config" // 导入新的 config 包
	"Air-Simulator/proto"
	"Air-Simulator/simulation"
	"flag" // [新增] 导入 flag 包
	"fmt"
	"log"
	"net"

	"google.golang.org/grpc"
)

func main() {
	// --- [新增] 定义命令行flag来指定端口 ---
	// 定义一个名为 "port" 的 flag，默认值为 "50051"，并提供描述信息
	port := flag.String("port", "50051", "gRPC server port")
	flag.Parse() // 解析命令行参数

	log.Println("=============================================")
	log.Println("======  Air-Ground Communication Simulation  ======")
	log.Println("======         (MARL Environment Mode)         ======")
	log.Println("=============================================")
	if config.EnableBackupChannel {
		log.Printf("加载配置: 双信道模式")
	} else {
		log.Printf("加载配置: 单信道模式")
	}
	log.Println("=============================================")

	// --- 1. 创建信道和通信系统 ---
	primaryChannel := simulation.NewChannel("Primary", config.PrimaryPMap, config.PrimaryTimeSlot)
	var backupChannel *simulation.Channel
	if config.EnableBackupChannel {
		backupChannel = simulation.NewChannel("Backup", config.BackupPMap, config.BackupTimeSlot)
	}

	commsSystem := simulation.NewCommunicationSystem(primaryChannel, backupChannel, config.SwitchoverProbs)
	commsSystem.StartDispatching() // 启动所有信道的调度器

	// --- 2. 创建地面站和飞机 ---
	groundControl := simulation.NewGroundControlCenter("GND_CTL_MAIN")
	go groundControl.StartListening(commsSystem) // 地面站现在自主运行

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

	// --- 3. 创建背景流量生成器 ---
	trafficGenerator := simulation.NewTrafficGenerator(commsSystem)
	trafficGenerator.Start() // 启动流量生成器
	log.Printf("🚦 背景流量生成器已启动。")

	// --- 4. 创建数据收集器实例 ---
	channelsToMonitor := []*simulation.Channel{primaryChannel}
	if config.EnableBackupChannel {
		channelsToMonitor = append(channelsToMonitor, backupChannel)
	}
	groundStationsToMonitor := []*simulation.GroundControlCenter{groundControl}
	dataCollector := collector.NewDataCollector(aircraftList, channelsToMonitor, groundStationsToMonitor)
	log.Println("📊 数据收集器已准备就绪。")

	// --- 5. 启动 gRPC 服务器并阻塞主线程 ---
	listenAddress := fmt.Sprintf(":%s", *port) // [修改] 使用flag的值来构建监听地址
	lis, err := net.Listen("tcp", listenAddress)
	if err != nil {
		log.Fatalf("❌ 无法监听端口 %s: %v", *port, err)
	}
	log.Printf("🚀 gRPC 服务器正在监听 %s, 等待 Python 客户端连接...", listenAddress)

	grpcServer := grpc.NewServer()

	// 创建 API 服务器实例，并传入所有需要的模拟组件
	apiServer := api.NewServer(commsSystem, aircraftList, dataCollector, trafficGenerator)

	// 注册服务
	proto.RegisterSimulatorServer(grpcServer, apiServer)

	// 启动服务
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("❌ gRPC 服务器启动失败: %v", err)
	}
}
