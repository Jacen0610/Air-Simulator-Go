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
type agent interface {
	Step(action simulation.AgentAction, comms *simulation.CommunicationSystem) float32
	GetObservation(comms *simulation.CommunicationSystem) simulation.AgentObservation
	Reset()
}

// Server 结构体实现了 gRPC 接口。
type Server struct {
	proto.UnimplementedSimulatorServer

	commsSystem      *simulation.CommunicationSystem
	aircraftAgent    agent
	aircraftList     []*simulation.Aircraft
	dataCollector    *collector.DataCollector
	trafficGenerator *simulation.TrafficGenerator

	simulationRunning atomic.Bool
	episodeCounter    atomic.Int64
}

// NewServer 是 Server 的构造函数。
func NewServer(
	comms *simulation.CommunicationSystem,
	aircrafts []*simulation.Aircraft,
	collector *collector.DataCollector,
	trafficGen *simulation.TrafficGenerator,
) *Server {
	if len(aircrafts) != 1 {
		log.Fatalf("错误: 服务器期望一个飞机智能体，但收到了 %d 个。", len(aircrafts))
	}

	s := &Server{
		commsSystem:      comms,
		aircraftAgent:    aircrafts[0],
		aircraftList:     aircrafts,
		dataCollector:    collector,
		trafficGenerator: trafficGen,
	}

	return s
}

// Step 是 gRPC Step 方法的实现。
func (s *Server) Step(ctx context.Context, req *proto.StepRequest) (*proto.StepResponse, error) {
	simAction := simulation.AgentAction(req.Action)
	reward := s.aircraftAgent.Step(simAction, s.commsSystem)
	obs := s.aircraftAgent.GetObservation(s.commsSystem)
	isDone := !s.simulationRunning.Load() && s.episodeCounter.Load() > 0

	state := &proto.AgentState{
		Observation: mapObservationToProto(obs),
		Reward:      reward,
		Done:        isDone,
	}

	return &proto.StepResponse{State: state}, nil
}

// Reset 是 gRPC Reset 方法的实现。
func (s *Server) Reset(ctx context.Context, req *proto.ResetRequest) (*proto.ResetResponse, error) {
	currentEpisode := s.episodeCounter.Add(1)
	log.Printf("🔄 [Episode %d] 收到 Reset 请求，正在重置并启动新一轮模拟...", currentEpisode)

	s.trafficGenerator.Reset()
	s.aircraftAgent.Reset()
	s.commsSystem.PrimaryChannel.ResetStats()
	if s.commsSystem.BackupChannel != nil {
		s.commsSystem.BackupChannel.ResetStats()
	}

	s.simulationRunning.Store(true)

	go func() {
		defer s.simulationRunning.Store(false)
		defer s.dataCollector.CollectAndSave(int(currentEpisode))
		simulation.RunSimulationSession(s.aircraftList)
		log.Printf("✅ [Episode %d] 飞行计划模拟已在后台完成。", currentEpisode)
	}()

	obs := s.aircraftAgent.GetObservation(s.commsSystem)
	initialState := &proto.AgentState{
		Observation: mapObservationToProto(obs),
		Reward:      0.0,
		Done:        false,
	}

	return &proto.ResetResponse{State: initialState}, nil
}

// mapObservationToProto 辅助函数
// 将 simulation 层的观测状态映射到 proto 层的消息结构。
func mapObservationToProto(obs simulation.AgentObservation) *proto.AgentObservation {
	return &proto.AgentObservation{
		IsChannelBusy:             obs.IsChannelBusy,
		HasDataToSend:             obs.HasDataToSend,
		OutboundQueueLength:       obs.OutboundQueueLength,
		TopMessageWaitTimeSeconds: obs.TopMessageWaitTimeSeconds,
		ConsecutiveIdleSteps:      obs.ConsecutiveIdleSteps,
		LastSendCausedCollision:   obs.LastSendCausedCollision,
		StepsSinceLastCollision:   obs.StepsSinceLastCollision,
		ChannelBusyRatio:          obs.ChannelBusyRatio,
	}
}
