// C:/workspace/go/Air-Simulator-Go/simulation/constants.go
package simulation

import "time"

// 全局模拟常量
const (
	// TimeSlot p-坚持 CSMA/CA 时隙
	TimeSlot = 320 * time.Millisecond

	// 飞行参数
	FlightDuration        = 30 * time.Minute // 飞行计划时常
	PosReportInterval     = 5 * time.Minute  // 位置报告间隔
	TaxiTime              = 4 * time.Minute  // 滑行时间
	FuelReportInterval    = 10 * time.Minute // 燃料报告间隔
	WeatherReportInterval = 8 * time.Minute  // 天气报告间隔

	// 飞机通信参数
	TransmissionTime = 80 * time.Millisecond
	AckTimeout       = 3000 * time.Millisecond
	MaxRetries       = 16

	// 地面站处理延迟
	ProcessingDelay = 200 * time.Millisecond
)
