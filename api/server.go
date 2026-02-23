// C:/workspace/go/Air-Simulator-Go/api/server.go
package api

import (
	"Air-Simulator/collector"
	"Air-Simulator/proto"
	"Air-Simulator/simulation"
	"context"
	"log"
	"sync/atomic"
	"time"
)

// agent 接口定义了智能体的行为。
type agent interface {
	Step(action simulation.AgentAction, comms *simulation.CommunicationSystem) float32
	GetObservation(comms *simulation.CommunicationSystem, simStartTime time.Time) simulation.AgentObservation
	Reset(episodeID int, startTime time.Time) // [修改] 增加参数
}

// Server 结构体实现了 gRPC 接口。
type Server struct {
	proto.UnimplementedSimulatorServer

	commsSystem      *simulation.CommunicationSystem
	aircraftAgent    agent
	aircraftList     []*simulation.Aircraft
	dataCollector    *collector.DataCollector
	trafficGenerator *simulation.TrafficGenerator

	simulationRunning   atomic.Bool
	episodeCounter      atomic.Int64
	simulationStartTime time.Time // [新增] 记录模拟开始时间
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
	obs := s.aircraftAgent.GetObservation(s.commsSystem, s.simulationStartTime) // [修改] 传递开始时间
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

	s.simulationStartTime = time.Now() // [修改] 记录新的模拟开始时间
	s.trafficGenerator.Reset()
	s.aircraftAgent.Reset(int(currentEpisode), s.simulationStartTime) // [修改] 传递参数
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

	obs := s.aircraftAgent.GetObservation(s.commsSystem, s.simulationStartTime) // [修改] 传递开始时间
	initialState := &proto.AgentState{
		Observation: mapObservationToProto(obs),
		Reward:      0.0,
		Done:        false,
	}

	return &proto.ResetResponse{State: initialState}, nil
}

// [核心重构] mapObservationToProto 辅助函数
// 将12维的 simulation 层观测状态映射到12维的 proto 层消息结构。
func mapObservationToProto(obs simulation.AgentObservation) *proto.AgentObservation {
	return &proto.AgentObservation{
		HasData:   obs.HasData,
		IsBusy:    obs.IsBusy,
		BusyDur:   obs.BusyDur,
		IdleDur:   obs.IdleDur,
		Ratio_1S:  obs.Ratio1s,
		Ratio_01S: obs.Ratio01s,
		WaitTime:  obs.WaitTime,
		QSize:     obs.QSize,
		LastAct:   obs.LastAct,
		IsColl:    obs.IsColl,
		CyclePos:  obs.CyclePos,
		DtStep:    obs.DtStep,
	}
}
