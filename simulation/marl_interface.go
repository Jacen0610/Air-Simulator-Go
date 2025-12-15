package simulation

// AgentAction 代表一个智能体在一个时间步内可以执行的离散动作。
type AgentAction int

const (
	// ActionWait 代表智能体选择等待，不执行任何操作。
	ActionWait AgentAction = iota // 值为 0
	// ActionSend 代表智能体尝试在主信道发送其最高优先级的消息。
	ActionSend // 值为 1
)

// 注: AgentObservation 结构体定义已移至 aircraft.go 文件，以解决重复定义问题。
