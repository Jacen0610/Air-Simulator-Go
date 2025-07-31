// C:/workspace/go/Air-Simulator-Go/api/server.go
package api

import (
	"Air-Simulator/collector"
	"Air-Simulator/config"
	"Air-Simulator/proto"
	"Air-Simulator/simulation"
	"context"
	"log"
	"sync"
	"sync/atomic"
)

// agent 接口统一定义了飞机和地面站的行为，方便在服务器中统一处理
type agent interface {
	Step(action simulation.AgentAction, comms *simulation.CommunicationSystem) float32
	GetObservation(comms *simulation.CommunicationSystem) simulation.AgentObservation
	Reset() // 使用新的、更彻底的 Reset 方法
}

// Server 结构体实现了在 simulator.proto 中定义的 SimulatorServer 接口。
type Server struct {
	proto.UnimplementedSimulatorServer // 必须嵌入，以实现向前兼容

	// --- 模拟组件 ---
	commsSystem    *simulation.CommunicationSystem
	aircraftList   []*simulation.Aircraft
	groundStations []*simulation.GroundControlCenter
	agents         map[string]agent // 使用 agent 接口来统一存储飞机和地面站
	dataCollector  *collector.DataCollector

	// --- 状态管理 ---
	agentMutex        sync.RWMutex
	simulationRunning atomic.Bool  // 标记飞行计划是否正在运行
	episodeCounter    atomic.Int64 // 用于记录 episode 编号
}

// NewServer 是 Server 的构造函数。
func NewServer(
	comms *simulation.CommunicationSystem,
	aircrafts []*simulation.Aircraft,
	groundStations []*simulation.GroundControlCenter,
	collector *collector.DataCollector,
) *Server {
	s := &Server{
		commsSystem:    comms,
		aircraftList:   aircrafts,
		groundStations: groundStations,
		agents:         make(map[string]agent),
		dataCollector:  collector,
	}

	// 将所有飞机注册为 agent
	for _, ac := range aircrafts {
		s.agents[ac.CurrentFlightID] = ac
	}
	// 将所有地面站注册为 agent
	for _, gcc := range groundStations {
		s.agents[gcc.ID] = gcc
	}

	return s
}

// Step 是 gRPC 的 Step 方法的实现。
func (s *Server) Step(ctx context.Context, req *proto.StepRequest) (*proto.StepResponse, error) {
	s.agentMutex.RLock()
	defer s.agentMutex.RUnlock()

	// 使用带锁的 map 来安全地并发写入奖励
	rewards := make(map[string]float32)
	var rewardsMutex sync.Mutex
	var wg sync.WaitGroup

	// 并发执行所有智能体的 Step 函数
	for id, action := range req.Actions {
		if agentInstance, ok := s.agents[id]; ok {
			wg.Add(1)
			go func(id string, agentInstance agent, action proto.Action) {
				defer wg.Done()
				// 将 proto 的 Action 转换为 simulation 的 AgentAction
				simAction := simulation.AgentAction(action - 1) // 减1是因为proto枚举从1开始
				reward := agentInstance.Step(simAction, s.commsSystem)

				rewardsMutex.Lock()
				rewards[id] = reward
				rewardsMutex.Unlock()
			}(id, agentInstance, action)
		}
	}
	wg.Wait()

	// 收集所有智能体的新状态
	newStates := make(map[string]*proto.AgentState)
	// 现在的 isDone 条件是安全的，因为它读取的是在 Reset 中被提前设置好的 simulationRunning 状态
	isDone := !s.simulationRunning.Load() && s.episodeCounter.Load() > 0

	for id, agentInstance := range s.agents {
		obs := agentInstance.GetObservation(s.commsSystem)
		newStates[id] = &proto.AgentState{
			Observation: mapObservationToProto(obs),
			Reward:      rewards[id],
			Done:        isDone,
		}
	}

	return &proto.StepResponse{States: newStates}, nil
}

// Reset 是 gRPC 的 Reset 方法的实现。
func (s *Server) Reset(ctx context.Context, req *proto.ResetRequest) (*proto.ResetResponse, error) {
	// 增加 episode 计数
	currentEpisode := s.episodeCounter.Add(1)
	log.Printf("🔄 [Episode %d] 收到 Reset 请求，正在重置并启动新一轮模拟...", currentEpisode)

	s.agentMutex.RLock()
	defer s.agentMutex.RUnlock()

	// 1. 重置所有智能体和信道的统计数据和状态
	for _, agentInstance := range s.agents {
		agentInstance.Reset()
	}
	s.commsSystem.PrimaryChannel.ResetStats()
	if s.commsSystem.BackupChannel != nil {
		s.commsSystem.BackupChannel.ResetStats()
	}

	// 在启动 goroutine 之前，就将模拟状态设置为 true。
	// 这就消除了竞态条件，确保任何紧随其后的 Step 请求都能看到正确的 "正在运行" 状态。
	s.simulationRunning.Store(true)

	// 2. 在后台启动新的飞行计划模拟（消息生成器）
	go func() {
		// defer 语句确保在 goroutine 结束时（即模拟完成后）执行清理工作
		defer s.simulationRunning.Store(false)
		defer s.dataCollector.CollectAndSave(int(currentEpisode))

		// 调用阻塞式的模拟函数。这个函数会运行约68分钟。
		simulation.RunSimulationSession(s.aircraftList)

		log.Printf("✅ [Episode %d] 飞行计划模拟已在后台完成。", currentEpisode)
	}()

	// 3. 获取所有智能体的初始状态并立即返回给 Python
	initialStates := make(map[string]*proto.AgentState)
	for id, agentInstance := range s.agents {
		obs := agentInstance.GetObservation(s.commsSystem)
		initialStates[id] = &proto.AgentState{
			Observation: mapObservationToProto(obs),
			Reward:      0.0,
			Done:        false,
		}
	}

	return &proto.ResetResponse{States: initialStates}, nil
}

// mapObservationToProto 是一个辅助函数，用于将 Go 的观测结构转换为 Protobuf 结构。
func mapObservationToProto(obs simulation.AgentObservation) *proto.AgentObservation {
	return &proto.AgentObservation{
		HasMessage:          obs.HasMessage,
		TopMessagePriority:  mapPriorityToProto(obs.TopMessagePriority),
		PrimaryChannelBusy:  obs.PrimaryChannelBusy,
		BackupChannelBusy:   obs.BackupChannelBusy,
		PendingAcksCount:    obs.PendingAcksCount,
		OutboundQueueLength: obs.OutboundQueueLength, // **[核心修改]**
	}
}

// mapPriorityToProto 是一个辅助函数，用于将 Go 的优先级字符串转换为 Protobuf 的枚举。
func mapPriorityToProto(p config.Priority) proto.Priority {
	switch p {
	case config.CriticalPriority:
		return proto.Priority_PRIORITY_CRITICAL
	case config.HighPriority:
		return proto.Priority_PRIORITY_HIGH
	case config.MediumPriority:
		return proto.Priority_PRIORITY_MEDIUM
	case config.LowPriority:
		return proto.Priority_PRIORITY_LOW
	default:
		return proto.Priority_PRIORITY_UNSPECIFIED
	}
}
