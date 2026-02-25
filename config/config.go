// C:/workspace/go/Air-Simulator-Go/config/config.go
package config

import (
	"time"
)

// ===================================================================
//                           随机性控制
// ===================================================================

const (
	// UseFixedSeed 控制是否使用固定的随机种子。
	// true: 使用下面的 RandomSeed 值，使得每次运行的随机事件序列完全相同，便于调试和模型评估。
	// false: 使用当前时间作为种子，使得每次运行都具有不同的随机性。
	UseFixedSeed = true

	// RandomSeed 是一个预设的种子值。仅在 UseFixedSeed 为 true 时生效。
	RandomSeed int64 = 42
)

type Priority string

const (
	HighPriority     Priority = "HIGH"
	CriticalPriority Priority = "CRITICAL"
	LowPriority      Priority = "LOW"
	MediumPriority   Priority = "Medium"
)

func (p Priority) Value() int {
	switch p {
	case CriticalPriority:
		return 4
	case HighPriority:
		return 3
	case MediumPriority:
		return 2
	case LowPriority:
		return 1
	default:
		return 0
	}
}

type PriorityPMap map[Priority]float64

// ===================================================================
//                           模拟总开关
// ===================================================================

// [修改后] EnableBackupChannel 控制是否启用备用信道。
// true: 启用双信道模式。
// false: 切换为单信道模式。
const EnableBackupChannel = false

// [新增] 流量生成器配置
const (
	// EnableTrafficWaveMode 控制背景流量模式的切换方式。
	// true: 波浪式循环 (Low <-> Stable <-> Peak <-> Burst)
	// false: 突变式循环 (Low -> Stable -> Peak -> Burst -> Low)
	EnableTrafficWaveMode = true
)

// ===================================================================
//                       P-Persistence & Channel Switching
// ===================================================================

// PrimaryPMap 定义了主信道的 p-坚持 概率。
var PrimaryPMap = PriorityPMap{
	CriticalPriority: 0.9,
	HighPriority:     0.7,
	MediumPriority:   0.4,
	LowPriority:      0.2,
}

// BackupPMap 定义了备用信道的 p-坚持 概率。
// (在单信道模式下，此设置无效)
var BackupPMap = PriorityPMap{
	CriticalPriority: 0.95,
	HighPriority:     0.8,
	MediumPriority:   0.2,
	LowPriority:      0.1,
}

// SwitchoverProbs 定义了切换到备用信道的概率。
// (在单信道模式下，此设置无效)
var SwitchoverProbs = map[Priority]float64{
	CriticalPriority: 1.0,
	HighPriority:     0.8,
	MediumPriority:   0.3,
	LowPriority:      0.05,
}

// ===================================================================
//                           通信参数
// ===================================================================

const (
	// 主信道的时隙长度
	PrimaryTimeSlot = 4500 * time.Microsecond
	// (在单信道模式下，此设置无效)
	BackupTimeSlot = 4500 * time.Microsecond

	// TransmissionTime 定义了发送一个标准ACARS报文所需的物理时间。
	TransmissionTime = 400 * time.Millisecond

	// AckTimeout 定义了发送方等待一个ACK报文的最大超时时间。
	AckTimeout = 3 * time.Second

	// MaxRetries 定义了报文失败后的最大重传次数。
	MaxRetries = 16

	// ProcessingDelay 模拟处理接收到的报文所需的时间。
	ProcessingDelay = 200 * time.Millisecond
)

// ===================================================================
//                           飞行计划参数
// ===================================================================

const (
	// FlightDuration 定义了飞机在空域内活动的总时长。
	FlightDuration = 20 * time.Minute

	// PosReportInterval 定义了例行位置报告的发送间隔。
	PosReportInterval = 2 * time.Minute

	// TaxiTime 定义了飞机在地面滑行所需的时间。
	TaxiTime = 1 * time.Minute

	// FuelReportInterval 定义了燃油状态报告的发送间隔。
	FuelReportInterval = 3 * time.Minute

	// WeatherReportInterval 定义了气象数据报告的发送间隔。
	WeatherReportInterval = 4 * time.Minute
)

const (
	BaseIntervalMultiple = 1
)
