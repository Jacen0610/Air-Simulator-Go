package simulation

import "Air-Simulator/config"

// AgentAction 代表一个智能体在一个时间步内可以执行的离散动作。
type AgentAction int

const (
	// ActionWait 代表智能体选择等待，继续监听信道。
	ActionWait AgentAction = iota
	// ActionSendPrimary 代表智能体尝试在主信道发送其最高优先级的消息。
	ActionSendPrimary
	// ActionSendBackup 代表智能体尝试在备用信道发送其最高优先级的消息。
	ActionSendBackup
)

// AgentObservation 代表一个智能体在特定时刻能够感知到的环境信息。
// 这就是神经网络的输入。
type AgentObservation struct {
	HasMessage          bool            `json:"has_message"`
	TopMessagePriority  config.Priority `json:"top_message_priority"`
	PrimaryChannelBusy  bool            `json:"primary_channel_busy"`
	BackupChannelBusy   bool            `json:"backup_channel_busy"`
	OutboundQueueLength int32           `json:"outbound_queue_length"`
	PendingAcksCount    int32           `json:"pending_acks_count"`
}

// StepResult 封装了单个智能体执行一个动作后的完整结果。
type StepResult struct {
	Observation AgentObservation
	Reward      float32
	Done        bool // 标志着一个 episode 是否结束
	Info        map[string]interface{}
}
