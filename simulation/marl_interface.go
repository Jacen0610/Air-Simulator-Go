package simulation

// AgentAction 代表一个智能体在一个时间步内可以执行的离散动作。
type AgentAction int

const (
	// ActionWait 代表智能体选择等待，继续监听信道。
	ActionWait AgentAction = iota // 值为 0
	// ActionSendPrimary 代表智能体尝试在主信道发送其最高优先级的消息。
	ActionSendPrimary // 值为 1
	// ActionSendBackup 代表智能体尝试在备用信道发送其最高优先级的消息。
	ActionSendBackup // 值为 2
)

// AgentObservation 代表一个智能体在特定时刻能够感知到的环境信息。
// 这就是神经网络的输入。
type AgentObservation struct {
	HasMessage bool `json:"has_message"`
	// [核心修改] 移除了 TopMessagePriority
	PrimaryChannelBusy  bool  `json:"primary_channel_busy"`
	BackupChannelBusy   bool  `json:"backup_channel_busy"`
	OutboundQueueLength int32 `json:"outbound_queue_length"`
	PendingAcksCount    int32 `json:"pending_acks_count"`
	// [核心修改] 新增了与奖励函数密切相关的状态
	TopMessageWaitTimeSeconds float32 `json:"top_message_wait_time_seconds"`
	IsRetransmission          bool    `json:"is_retransmission"`
}

// StepResult 封装了单个智能体执行一个动作后的完整结果。
type StepResult struct {
	Observation AgentObservation
	Reward      float32
	Done        bool // 标志着一个 episode 是否结束
	Info        map[string]interface{}
}
