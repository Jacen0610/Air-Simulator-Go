package simulation

// [修改后] AgentAction 代表一个智能体在一个时间步内可以执行的离散动作。
// 简化为2个动作，以适应单信道模式。
type AgentAction int

const (
	// ActionWait 代表智能体选择等待，不执行任何操作。
	ActionWait AgentAction = iota // 值为 0
	// ActionSend 代表智能体尝试在主信道发送其最高优先级的消息。
	ActionSend // 值为 1
)

// AgentObservation 代表一个智能体在特定时刻能够感知到的环境信息。
// 这将作为强化学习模型的输入。
// (此结构体维持原样，因为它与 .proto 文件中的定义匹配)
type AgentObservation struct {
	HasMessage                bool    `json:"has_message"`
	PrimaryChannelBusy        bool    `json:"primary_channel_busy"`
	BackupChannelBusy         bool    `json:"backup_channel_busy"`
	OutboundQueueLength       int32   `json:"outbound_queue_length"`
	PendingAcksCount          int32   `json:"pending_acks_count"`
	TopMessageWaitTimeSeconds float32 `json:"top_message_wait_time_seconds"`
	IsRetransmission          bool    `json:"is_retransmission"`
}

// [移除] StepResult 结构体已被移除，因为它在 gRPC 服务实现中不是必需的。
