package simulation

// AgentAction 代表一个智能体在一个时间步内可以执行的离散动作。
type AgentAction int

const (
	// ActionWait 代表智能体选择等待，不执行任何操作。
	ActionWait AgentAction = iota // 值为 0
	// ActionSend 代表智能体尝试在主信道发送其最高优先级的消息。
	ActionSend // 值为 1
)

// AgentObservation 定义了强化学习代理的观测状态
type AgentObservation struct {
	IsChannelBusy             float32 `json:"is_channel_busy"`               // 1. 信道是否忙碌
	HasDataToSend             float32 `json:"has_data_to_send"`              // 2. 自身是否有数据待发送
	OutboundQueueLength       float32 `json:"outbound_queue_length"`         // 3. 发件箱队列的长度
	TopMessageWaitTimeSeconds float32 `json:"top_message_wait_time_seconds"` // 4. 队首消息的等待时间(秒)
	ConsecutiveIdleSteps      float32 `json:"consecutive_idle_steps"`        // 5. 连续空闲步数
	LastSendCausedCollision   float32 `json:"last_send_caused_collision"`    // 6. 上一次发送是否导致碰撞
	StepsSinceLastCollision   float32 `json:"steps_since_last_collision"`    // 7. 距离上次碰撞的步数
	ChannelBusyRatio          float32 `json:"channel_busy_ratio"`            // 8. 信道拥堵率
}
