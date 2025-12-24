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
	// --- 核心状态 (Core Status) ---
	HasData float32 `json:"has_data"` // 1. 发件箱是否有消息待发 (1.0 for yes, 0.0 for no)
	IsBusy  float32 `json:"is_busy"`  // 2. 当前信道是否被背景流量占用 (1.0 for yes, 0.0 for no)

	// --- 信道节律 (Channel Rhythm) ---
	BusyDur  float32 `json:"busy_dur"`  // 3. 连续忙碌的物理时间(秒), 归一化时超过1s的全部截断成1s
	IdleDur  float32 `json:"idle_dur"`  // 4. 连续空闲的物理时间(秒), 归一化时超过1s的全部截断成1s
	Ratio1s  float32 `json:"ratio_1s"`  // 5. 过去 1.0s 内信道被占用的比例 (0.0 ~ 1.0)
	Ratio01s float32 `json:"ratio_01s"` // 6. 过去 0.1s 内信道被占用的比例 (0.0 ~ 1.0)

	// --- 问题紧迫性 (Urgency) ---
	WaitTime float32 `json:"wait_time"` // 7. 队首数据包已等待的物理时间, 归一化时超过5s全部截断成5s
	QSize    float32 `json:"q_size"`    // 8. 当前待发送队列长度, 归一化时超过5的全截断成5

	// --- 自我行为反思 (Self-Reflection) ---
	LastAct float32 `json:"last_act"` // 9. 上一回合执行的动作 (0 for Wait, 1 for Send)
	IsColl  float32 `json:"is_coll"`  // 10. 本次决策过程是否触发了碰撞锁定 (1.0 for yes, 0.0 for no)

	// --- 全局与系统上下文 (Global & System Context) ---
	CyclePos float32 `json:"cycle_pos"` // 11. 全局时钟, 模拟器启动后的秒数对360s取模, 再归一化
	DtStep   float32 `json:"dt_step"`   // 12. 距离上次决策的物理时间(秒), 归一化时用0.5s为最大值
}
