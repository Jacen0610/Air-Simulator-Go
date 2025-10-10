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
	MediumPriority   Priority = "MEDIUM"
)

type PriorityPMap map[Priority]float64

// ===================================================================
//                           模拟总开关
// ===================================================================

// ===================================================================
//                       P-Persistence & Channel Switching
// ===================================================================

var CSMA_PLAINE = 0.05
var CSMA_CHANNEL = 0.05

// ===================================================================
//                           通信参数
// ===================================================================

const (

	// 主、备用信道的时隙长度
	PrimaryTimeSlot = 4500 * time.Microsecond
	BackupTimeSlot  = 4500 * time.Microsecond

	// TransmissionTime 定义了发送一个标准ACARS报文所需的物理时间。
	TransmissionTime = 400 * time.Millisecond

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
	PosReportInterval = 2 * time.Minute

	// TaxiTime 定义了飞机在地面滑行所需的时间。
	TaxiTime = 1 * time.Minute

	// FuelReportInterval 定义了燃油状态报告的发送间隔。
	FuelReportInterval = 3 * time.Minute

	// WeatherReportInterval 定义了气象数据报告的发送间隔。
	WeatherReportInterval = 4 * time.Minute
)
