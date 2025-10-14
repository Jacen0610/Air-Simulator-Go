// C:/workspace/go/Air-Simulator-Go/api/server.go
package api

import (
	"Air-Simulator/collector"
	"Air-Simulator/proto"
	"Air-Simulator/simulation"
	"context"
	"log"
	"sync/atomic"
)

// agent 接口定义了智能体的行为。
// 在当前单智能体场景下，这主要指代飞机。
type agent interface {
	Step(action simulation.AgentAction, comms *simulation.CommunicationSystem) float32
	GetObservation(comms *simulation.CommunicationSystem) simulation.AgentObservation
	Reset()
}

// [修改后] Server 结构体实现了新的单智能体 gRPC 接口。
type Server struct {
	proto.UnimplementedSimulatorServer

	// --- 模拟组件 ---
	commsSystem      *simulation.CommunicationSystem
	aircraftAgent    agent                  // [修改] 直接引用唯一的飞机智能体
	aircraftList     []*simulation.Aircraft // [保留] 用于传递给 simulation.RunSimulationSession
	dataCollector    *collector.DataCollector
	trafficGenerator *simulation.TrafficGenerator // [新增] 背景流量生成器

	// --- 状态管理 ---
	simulationRunning atomic.Bool
	episodeCounter    atomic.Int64
}

// [修改后] NewServer 是 Server 的构造函数，适配单智能体场景。
func NewServer(
	comms *simulation.CommunicationSystem,
	aircrafts []*simulation.Aircraft,
	collector *collector.DataCollector,
	trafficGen *simulation.TrafficGenerator, // [新增] 接收流量生成器
) *Server {
	if len(aircrafts) != 1 {
		// 在单智能体模式下，我们期望只有一个飞机实例。
		// 使用 log.Fatalf 会导致程序立即退出，这在初始化阶段是合理的。
		log.Fatalf("错误: 服务器期望一个飞机智能体，但收到了 %d 个。", len(aircrafts))
	}

	s := &Server{
		commsSystem:      comms,
		aircraftAgent:    aircrafts[0], // [修改] 直接将第一个（也是唯一一个）飞机设为 agent
		aircraftList:     aircrafts,
		dataCollector:    collector,
		trafficGenerator: trafficGen, // [新增] 存储流量生成器实例
	}

	return s
}

// [修改后] Step 是 gRPC Step 方法的实现，适配单智能体。
func (s *Server) Step(ctx context.Context, req *proto.StepRequest) (*proto.StepResponse, error) {
	// 将 proto 的 Action (0,1) 转换为 simulation 的 AgentAction (0,1)
	simAction := simulation.AgentAction(req.Action)

	// 为唯一的智能体执行 Step
	reward := s.aircraftAgent.Step(simAction, s.commsSystem)

	// 获取新状态
	obs := s.aircraftAgent.GetObservation(s.commsSystem)

	// 检查模拟是否已在后台完成
	isDone := !s.simulationRunning.Load() && s.episodeCounter.Load() > 0

	// 构建响应
	state := &proto.AgentState{
		Observation: mapObservationToProto(obs),
		Reward:      reward,
		Done:        isDone,
	}

	return &proto.StepResponse{State: state}, nil
}

// [修改后] Reset 是 gRPC Reset 方法的实现，适配单智能体。
func (s *Server) Reset(ctx context.Context, req *proto.ResetRequest) (*proto.ResetResponse, error) {
	currentEpisode := s.episodeCounter.Add(1)
	log.Printf("🔄 [Episode %d] 收到 Reset 请求，正在重置并启动新一轮模拟...", currentEpisode)

	// 1. 重置所有模拟组件
	s.trafficGenerator.Reset() // [新增] 重置背景流量生成器
	s.aircraftAgent.Reset()
	s.commsSystem.PrimaryChannel.ResetStats()
	if s.commsSystem.BackupChannel != nil {
		s.commsSystem.BackupChannel.ResetStats()
	}

	// 2. 将模拟状态设置为 true
	s.simulationRunning.Store(true)

	// 3. 在后台启动新的飞行计划模拟
	go func() {
		defer s.simulationRunning.Store(false)
		defer s.dataCollector.CollectAndSave(int(currentEpisode))

		// 调用阻塞式的模拟函数
		simulation.RunSimulationSession(s.aircraftList)

		log.Printf("✅ [Episode %d] 飞行计划模拟已在后台完成。", currentEpisode)
	}()

	// 4. 获取智能体的初始状态并立即返回
	obs := s.aircraftAgent.GetObservation(s.commsSystem)
	initialState := &proto.AgentState{
		Observation: mapObservationToProto(obs),
		Reward:      0.0,
		Done:        false,
	}

	return &proto.ResetResponse{State: initialState}, nil
}

// mapObservationToProto 辅助函数 (无变化)
func mapObservationToProto(obs simulation.AgentObservation) *proto.AgentObservation {
	return &proto.AgentObservation{
		HasMessage:                obs.HasMessage,
		PrimaryChannelBusy:        obs.PrimaryChannelBusy,
		BackupChannelBusy:         obs.BackupChannelBusy,
		PendingAcksCount:          obs.PendingAcksCount,
		OutboundQueueLength:       obs.OutboundQueueLength,
		TopMessageWaitTimeSeconds: obs.TopMessageWaitTimeSeconds,
		IsRetransmission:          obs.IsRetransmission,
	}
}
