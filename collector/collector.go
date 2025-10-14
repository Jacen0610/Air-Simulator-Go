// C:/workspace/go/Air-Simulator-Go/collector/collector.go
package collector

import (
	"Air-Simulator/simulation"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/xuri/excelize/v2"
)

// DataCollector 结构体负责收集和记录模拟过程中的所有相关数据。
// 它现在是一个被动工具，在需要时被调用。
type DataCollector struct {
	aircrafts      []*simulation.Aircraft
	channels       []*simulation.Channel
	groundStations []*simulation.GroundControlCenter
}

// NewDataCollector 是 DataCollector 的构造函数。
func NewDataCollector(
	aircrafts []*simulation.Aircraft,
	channels []*simulation.Channel,
	groundStations []*simulation.GroundControlCenter,
) *DataCollector {
	return &DataCollector{
		aircrafts:      aircrafts,
		channels:       channels,
		groundStations: groundStations,
	}
}

// CollectAndSave 在一个 episode 结束后被调用，负责收集该回合的所有数据并保存到唯一的 Excel 文件中。
func (dc *DataCollector) CollectAndSave(episodeNumber int) {
	log.Printf("📊 [Episode %d] 开始收集数据并保存到 Excel...", episodeNumber)

	f := excelize.NewFile()
	defer func() {
		if err := f.Close(); err != nil {
			log.Printf("❌ [Episode %d] 关闭Excel文件时出错: %v", episodeNumber, err)
		}
	}()

	// 为不同类型的数据创建工作表
	aircraftSheet, channelSheet, groundSheet, waitTimeSheet := "Aircraft_Stats", "Channel_Stats", "GroundControl_Stats", "Wait_Time_Distribution"
	f.NewSheet(aircraftSheet)
	f.NewSheet(channelSheet)
	f.NewSheet(groundSheet)
	f.NewSheet(waitTimeSheet) // 新增工作表
	f.DeleteSheet("Sheet1")

	// 写入表头
	dc.writeHeaders(f, aircraftSheet, channelSheet, groundSheet)

	// 收集并写入所有统计数据
	dc.recordAllStats(f, aircraftSheet, channelSheet, groundSheet)
	dc.recordWaitTimeDistribution(f, waitTimeSheet) // 写入等待时间分布

	// --- 保存文件 ---
	// 设置文件名，确保每个 episode 的报告都是独立的
	fileName := fmt.Sprintf("simulation_report_episode_%d.xlsx", episodeNumber)
	fullPath := filepath.Join("report", fileName)

	// 在保存文件之前，确保目标目录存在
	reportDir := filepath.Dir(fullPath)
	if err := os.MkdirAll(reportDir, 0755); err != nil {
		log.Printf("❌ [Episode %d] 错误: 无法创建报告目录 '%s': %v", episodeNumber, reportDir, err)
		return
	}

	// 保存文件
	if err := f.SaveAs(fullPath); err != nil {
		log.Printf("❌ [Episode %d] 错误: 无法保存 Excel 报告文件: %v", episodeNumber, err)
	} else {
		log.Printf("✅ [Episode %d] 模拟数据报告已成功保存到: %s", episodeNumber, fullPath)
	}
}

// writeHeaders 负责向Excel文件写入表头。
func (dc *DataCollector) writeHeaders(f *excelize.File, aircraftSheet, channelSheet, groundSheet string) {
	headersAircraft := []string{"航班号", "成功传输", "重传", "尝试传输", "碰撞次数", "碰撞率 (%)", "平均等待时间 (ms)", "请求信道", "失败请求信道", "请求信道失败率 (%)", "未发送消息数"}
	_ = f.SetSheetRow(aircraftSheet, "A1", &headersAircraft)

	headersChannel := []string{"信道", "是否启用", "成功传输", "信道使用时间 (ms)", "信道使用率 (%)"}
	_ = f.SetSheetRow(channelSheet, "A1", &headersChannel)

	headersGround := []string{"地面站名", "成功传输", "尝试传输", "碰撞次数", "碰撞率 (%)", "平均等待时间 (ms)", "请求信道", "失败请求信道", "请求信道失败率 (%)"}
	_ = f.SetSheetRow(groundSheet, "A1", &headersGround)
}

// recordAllStats 一次性收集所有组件的最终统计数据。
func (dc *DataCollector) recordAllStats(f *excelize.File, aircraftSheet, channelSheet, groundSheet string) {
	// 记录飞机数据
	for i, ac := range dc.aircrafts {
		stats := ac.GetRawStats()
		var collisionRate, rqFailRate float64
		if stats.TotalTxAttempts > 0 {
			collisionRate = (float64(stats.TotalCollisions) / float64(stats.TotalTxAttempts)) * 100
		}
		if stats.TotalRqTunnel > 0 {
			rqFailRate = (float64(stats.TotalFailRqTunnel) / float64(stats.TotalRqTunnel)) * 100
		}
		var avgWaitTimeMs float64
		if (stats.SuccessfulTx + stats.TotalRetries) > 0 {
			avgWaitTimeMs = float64(stats.TotalWaitTime.Milliseconds()) / float64(stats.SuccessfulTx+stats.TotalRetries)
		}
		rowData := []interface{}{
			ac.CurrentFlightID, stats.SuccessfulTx, stats.TotalRetries, stats.TotalTxAttempts, stats.TotalCollisions, collisionRate,
			avgWaitTimeMs, stats.TotalRqTunnel, stats.TotalFailRqTunnel, rqFailRate, stats.UnsentMessages,
		}
		_ = f.SetSheetRow(aircraftSheet, fmt.Sprintf("A%d", i+2), &rowData)
	}

	// 记录信道数据
	// 注意：这里的总时长是基于一个典型的飞行计划估算的，因为收集器不再自己计时
	const typicalSimDuration = 68 * time.Minute
	for i, ch := range dc.channels {
		if ch == nil {
			rowData := []interface{}{"Backup (Disabled)", "Disabled", 0, 0, 0.0}
			_ = f.SetSheetRow(channelSheet, fmt.Sprintf("A%d", i+2), &rowData)
			continue
		}
		stats := ch.GetRawStats()
		utilization := (float64(stats.TotalBusyTime) / float64(typicalSimDuration)) * 100
		rowData := []interface{}{
			ch.ID, "Enabled", stats.TotalMessagesTransmitted, stats.TotalBusyTime.Milliseconds(), utilization,
		}
		_ = f.SetSheetRow(channelSheet, fmt.Sprintf("A%d", i+2), &rowData)
	}

	// 记录地面站数据
	for i, gcc := range dc.groundStations {
		stats := gcc.GetRawStats()
		var collisionRate, rqFailRate float64
		if stats.TotalTxAttempts > 0 {
			collisionRate = (float64(stats.TotalCollisions) / float64(stats.TotalTxAttempts)) * 100
		}
		if stats.TotalRqTunnel > 0 {
			rqFailRate = (float64(stats.TotalFailRqTunnel) / float64(stats.TotalRqTunnel)) * 100
		}
		var avgWaitTimeMs float64
		if stats.SuccessfulTx > 0 {
			avgWaitTimeMs = float64(stats.TotalWaitTimeNs.Milliseconds()) / float64(stats.SuccessfulTx)
		}
		rowData := []interface{}{
			gcc.ID, stats.SuccessfulTx, stats.TotalTxAttempts, stats.TotalCollisions, collisionRate,
			avgWaitTimeMs, stats.TotalRqTunnel, stats.TotalFailRqTunnel, rqFailRate,
		}
		_ = f.SetSheetRow(groundSheet, fmt.Sprintf("A%d", i+2), &rowData)
	}
}

// recordWaitTimeDistribution 收集所有飞机和地面站的等待时间并写入专用工作表
func (dc *DataCollector) recordWaitTimeDistribution(f *excelize.File, sheetName string) {
	// 1. 写入表头
	header := []string{"WaitTime (ms)"}
	_ = f.SetSheetRow(sheetName, "A1", &header)

	// 2. 收集所有等待时间
	var allWaitTimes []time.Duration
	// 从飞机获取等待时间
	for _, ac := range dc.aircrafts {
		waitTimes := ac.GetWaitTimes()
		allWaitTimes = append(allWaitTimes, waitTimes...)
	}

	// 3. 逐行写入数据
	for i, wt := range allWaitTimes {
		// 将 time.Duration 转换为毫秒
		waitTimeMs := float64(wt.Nanoseconds()) / 1e6
		rowData := []interface{}{waitTimeMs}
		cell, _ := excelize.CoordinatesToCellName(1, i+2) // A2, A3, ...
		_ = f.SetSheetRow(sheetName, cell, &rowData)
	}
}
