// C:/workspace/go/Air-Simulator-Go/config/config.go
package config

import (
	"time"
)

type Priority string

const (
	HighPriority     Priority = "HIGH"
	CriticalPriority Priority = "CRITICAL"
	LowPriority      Priority = "LOW"
	MediumPriority   Priority = "Medium"
)

type PriorityPMap map[Priority]float64

// ===================================================================
//                           模拟总开关
// ===================================================================

// EnableBackupChannel 控制是否启用备用信道。
// true: 启用双信道模式，高优先级消息在主信道忙时可使用备用信道。
// false: 恢复为传统的单信道模式。
const EnableBackupChannel = true

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
// 它被配置为只高效处理高优先级消息。
var BackupPMap = PriorityPMap{
	CriticalPriority: 0.95,
	HighPriority:     0.8,
	MediumPriority:   0.2,
	LowPriority:      0.1,
}

// SwitchoverProbs 定义了当主信道忙碌时，不同优先级的消息切换到备用信道的概率。
var SwitchoverProbs = map[Priority]float64{
	CriticalPriority: 1.0,  // 紧急报文: 100% 尝试切换
	HighPriority:     0.8,  // 高优先级报文: 80% 尝试切换
	MediumPriority:   0.3,  // 中优先级报文: 30% 尝试切换
	LowPriority:      0.05, // 低优先级报文: 几乎不切换
}

// ===================================================================
//                           通信参数
// ===================================================================

const (

	// 主、备用信道的时隙长度
	PrimaryTimeSlot = 320 * time.Millisecond
	BackupTimeSlot  = 320 * time.Millisecond

	// TransmissionTime 定义了发送一个标准ACARS报文所需的物理时间。
	TransmissionTime = 80 * time.Millisecond

	// AckTimeout 定义了发送方等待一个ACK报文的最大超时时间。
	AckTimeout = 3 * time.Second // 增加了一些余量

	// MaxRetries 定义了一个报文在因超时或碰撞失败后，允许的最大重传次数。
	MaxRetries = 16

	// ProcessingDelay 模拟地面站或飞机处理接收到的报文所需的时间。
	ProcessingDelay = 200 * time.Millisecond
)

// ===================================================================
//                           飞行计划参数
// ===================================================================

const (
	// FlightDuration 定义了每个飞行计划中，飞机在空域内活动的总时长。
	FlightDuration = 30 * time.Minute

	// PosReportInterval 定义了例行位置报告的发送间隔。
	PosReportInterval = 5 * time.Minute

	// TaxiTime 定义了飞机在地面滑行所需的时间。
	TaxiTime = 4 * time.Minute

	// FuelReportInterval 定义了燃油状态报告的发送间隔。
	FuelReportInterval = 10 * time.Minute

	// WeatherReportInterval 定义了气象数据报告的发送间隔。
	WeatherReportInterval = 8 * time.Minute
)
