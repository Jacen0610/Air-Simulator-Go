// C:/workspace/go/Air-Simulator-Go/environment/server.go
package environment

import (
	"Air-Simulator/collector" // 导入 collector 包
	"Air-Simulator/protos"
	"Air-Simulator/simulation"
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

const (
	// 定义RL的每个时间步代表多长的虚拟仿真时间
	stepDuration = 1 * time.Minute
)

// Server 结构体实现了 gRPC 服务，并持有整个仿真世界的状态
type Server struct {
	protos.UnimplementedRLEnvironmentServer

	config Config

	// 仿真核心组件
	commSystem    *simulation.CommunicationSystem
	aircraftList  []*simulation.Aircraft
	groundControl *simulation.GroundControlCenter
	collector     *collector.DataCollector // collector 实例

	// 仿真控制
	simWg                 *sync.WaitGroup
	simDoneChan           chan struct{} // 用于接收模拟结束的信号
	isDone                bool
	currentSimTimeMinutes int // 新增: 跟踪虚拟仿真时间

	lastStepStats stepStats
}

// NewServer 创建一个新的环境服务器，并接收一个配置对象
func NewServer(config Config) *Server {
	s := &Server{
		config: config,
	}
	return s
}

// initializeSimulation 负责重置和初始化整个仿真世界
func (s *Server) initializeSimulation() {
	log.Println("--- Initializing/Resetting Simulation Environment ---")
	s.isDone = false
	s.simDoneChan = make(chan struct{}) // 创建新的信号 channel
	s.currentSimTimeMinutes = 0         // 重置虚拟时间
	s.lastStepStats = stepStats{}
	s.simWg = &sync.WaitGroup{}

	// ... (创建 pMap, 信道, 通信系统, 地面站, 飞机的代码保持不变) ...
	initialPMap := simulation.PriorityPMap{
		simulation.CriticalPriority: 0.9, simulation.HighPriority: 0.7,
		simulation.MediumPriority: 0.4, simulation.LowPriority: 0.2,
	}
	primaryChannel := simulation.NewChannel(initialPMap)
	go primaryChannel.StartDispatching()
	if s.config.EnableDualChannel {
		log.Println("模式: 启用双信道通信 (主用/备用)")
		backupChannel := simulation.NewChannel(initialPMap)
		go backupChannel.StartDispatching()
		s.commSystem = simulation.NewCommunicationSystem(primaryChannel, backupChannel)
	} else {
		log.Println("模式: 启用单信道通信")
		s.commSystem = simulation.NewCommunicationSystem(primaryChannel, nil)
	}
	s.groundControl = simulation.NewGroundControlCenter("GND_CTL_SEU")
	go s.groundControl.StartListening(s.commSystem)
	s.aircraftList = make([]*simulation.Aircraft, 20)
	for i := 0; i < 20; i++ {
		icao := fmt.Sprintf("A%d", 70000+i)
		reg := fmt.Sprintf("B-%d", 6000+i)
		flightID := fmt.Sprintf("CES-%d", 1001+i)
		aircraft := simulation.NewAircraft(icao, reg, "A320neo", "Airbus", "MSN1234"+fmt.Sprintf("%d", i), "CES")
		aircraft.CurrentFlightID = flightID
		s.aircraftList[i] = aircraft
		go aircraft.StartListening(s.commSystem)
	}

	// --- 核心修改: 启动飞行计划和结束监视器 ---
	// 1. 创建 collector 实例
	s.collector = collector.NewDataCollector(s.aircraftList, s.groundControl, s.commSystem)
	// 2. 初始化本轮 Episode 的报告
	s.collector.InitializeEpisode()

	// 3. 启动所有飞行计划
	simulation.RunSimulationSession(s.simWg, s.commSystem, s.aircraftList)

	// 4. 启动一个独立的 goroutine 来等待所有飞行计划完成
	go s.waitForSimulationEnd()
}

// waitForSimulationEnd 会阻塞直到所有飞行计划完成，然后发出信号。
func (s *Server) waitForSimulationEnd() {
	s.simWg.Wait()       // 等待所有 wg.Done() 被调用
	close(s.simDoneChan) // 关闭 channel，这是一个广播信号
	log.Println("🏁 所有飞行计划已在后台完成。")
}

// Reset 实现了 gRPC 的 Reset 方法
func (s *Server) Reset(ctx context.Context, req *protos.ResetRequest) (*protos.State, error) {
	s.initializeSimulation()
	return &protos.State{}, nil
}

// Step 实现了 gRPC 的 Step 方法，这是 RL 的核心
func (s *Server) Step(ctx context.Context, action *protos.Action) (*protos.StepResponse, error) {
	// 检查模拟是否在上一步已经结束
	if s.isDone {
		log.Println("Simulation is done, resetting for a new episode...")
		s.initializeSimulation()
		return &protos.StepResponse{
			NextState: &protos.State{},
			Reward:    0,
			Done:      true, // 告知 Python 端上一个 episode 确实结束了
		}, nil
	}

	// 检查模拟是否在本步结束
	select {
	case <-s.simDoneChan:
		// 如果能从 simDoneChan 接收到信号，说明模拟已完成
		s.isDone = true
		log.Println("Episode finished: All flight plans completed.")
		// 保存最终报告
		s.collector.SaveFinalReport()
		// 返回最终状态和 Done=true
		finalState, finalReward := s.calculateIncrementalMetrics()
		return &protos.StepResponse{
			NextState: finalState,
			Reward:    finalReward,
			Done:      true,
		}, nil
	default:
		// 默认情况：模拟尚未结束，继续正常执行
	}

	// 新增: 在应用动作之前，立即记录 Agent 的决策
	s.collector.CollectActionData(s.currentSimTimeMinutes, action)

	// 1. 应用智能体的动作
	newPMap := simulation.PriorityPMap{
		simulation.CriticalPriority: action.PCritical, simulation.HighPriority: action.PHigh,
		simulation.MediumPriority: action.PMedium, simulation.LowPriority: action.PLow,
	}
	newTimeSlot := time.Duration(action.TimeSlotMs) * time.Millisecond
	s.commSystem.PrimaryChannel.UpdatePMap(newPMap)
	if s.commSystem.BackupChannel != nil {
		s.commSystem.BackupChannel.UpdatePMap(newPMap)
	}
	s.commSystem.UpdateCurrentTimeSlot(newTimeSlot)

	// 2. 计算状态和奖励
	nextState, reward := s.calculateIncrementalMetrics()

	// 3. 推进仿真时间
	time.Sleep(stepDuration)
	s.currentSimTimeMinutes += int(stepDuration.Minutes())

	// 4. 检查是否需要进行周期性数据收集
	if s.currentSimTimeMinutes%5 == 0 && s.currentSimTimeMinutes > 0 {
		s.collector.CollectPeriodicData(s.currentSimTimeMinutes)
	}

	return &protos.StepResponse{
		NextState: nextState,
		Reward:    reward,
		Done:      s.isDone, // 在此路径下，isDone 始终为 false
	}, nil
}

// calculateStepMetrics 封装了计算状态和奖励的逻辑，以避免代码重复
func (s *Server) calculateIncrementalMetrics() (*protos.State, float64) {
	// 1. 获取当前的累积统计数据
	currentStats := s.collectAllStats()

	// 2. 计算自上一步以来的增量
	deltaSuccessfulTx := currentStats.TotalSuccessfulTx - s.lastStepStats.TotalSuccessfulTx
	deltaAttempts := currentStats.TotalTxAttempts - s.lastStepStats.TotalTxAttempts
	deltaCollisions := currentStats.TotalCollisions - s.lastStepStats.TotalCollisions
	deltaRetries := currentStats.TotalRetries - s.lastStepStats.TotalRetries
	deltaWaitTime := currentStats.TotalWaitTime - s.lastStepStats.TotalWaitTime

	// 3. 基于增量数据计算指标
	var collisionRate float64
	if deltaAttempts > 0 {
		collisionRate = float64(deltaCollisions) / float64(deltaAttempts)
	}

	var avgWaitTimeMs float64
	if deltaSuccessfulTx > 0 {
		avgWaitTimeMs = float64(deltaWaitTime.Milliseconds()) / float64(deltaSuccessfulTx)
	}

	// 4. 基于增量指标计算奖励
	// 这个奖励现在清晰地反映了过去一分钟的表现
	reward := (1.0 * float64(deltaSuccessfulTx)) - (100.0 * collisionRate) - (0.005 * avgWaitTimeMs) - (5 * float64(deltaRetries))

	// 5. 组装下一个状态 (状态本身可以是累积的，也可以是增量的，这里使用累积的更稳定)
	nextState := &protos.State{
		Throughput:                float64(currentStats.TotalSuccessfulTx),
		AvgCollisionRate:          collisionRate, // 返回的是本步的碰撞率
		AvgWaitTimeMs:             avgWaitTimeMs, // 返回的是本步的平均等待时间
		PrimaryChannelUtilization: s.commSystem.PrimaryChannel.GetAndResetUtilization(stepDuration),
		TotalRetries:              float64(currentStats.TotalRetries),
	}
	if s.commSystem.BackupChannel != nil {
		nextState.BackupChannelUtilization = s.commSystem.BackupChannel.GetAndResetUtilization(stepDuration)
	}

	// 6. 更新 lastStepStats 以备下一步使用
	s.lastStepStats = currentStats

	return nextState, reward
}

// --- 辅助统计结构和函数 (保持不变) ---
type stepStats struct {
	TotalSuccessfulTx uint64
	TotalTxAttempts   uint64
	TotalCollisions   uint64
	TotalRetries      uint64
	TotalWaitTime     time.Duration
}

func (s *Server) collectAllStats() stepStats {
	var totalStats stepStats
	for _, ac := range s.aircraftList {
		stats := ac.GetRawStats()
		totalStats.TotalSuccessfulTx += stats.SuccessfulTx
		totalStats.TotalTxAttempts += stats.TotalTxAttempts
		totalStats.TotalCollisions += stats.TotalCollisions
		totalStats.TotalRetries += stats.TotalRetries
		totalStats.TotalWaitTime += stats.TotalWaitTime
	}
	gcStats := s.groundControl.GetRawStats()
	totalStats.TotalSuccessfulTx += gcStats.SuccessfulTx
	totalStats.TotalTxAttempts += gcStats.TotalTxAttempts
	totalStats.TotalCollisions += gcStats.TotalCollisions
	totalStats.TotalWaitTime += gcStats.TotalWaitTimeNs
	return totalStats
}
