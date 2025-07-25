// C:/workspace/go/Air-Simulator-Go/collector/collector.go
package collector

import (
	// collector 只依赖于 simulation 包中定义的类型和接口，不关心其内部逻辑
	"Air-Simulator/simulation"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/xuri/excelize/v2"
)

// collectionInterval 定义了数据收集和写入Excel的时间间隔。
const collectionInterval = 10 * time.Minute

// DataCollector 是一个独立的、解耦的数据记录器。
// 它在初始化时接收所有需要监控的对象，并在模拟期间定期记录它们的原始统计数据。
type DataCollector struct {
	aircrafts      []*simulation.Aircraft
	channels       []*simulation.Channel
	groundStations []*simulation.GroundControlCenter
	filename       string
	wg             *sync.WaitGroup
	done           <-chan struct{}
	startTime      time.Time
}

// NewDataCollector 创建一个新的数据收集器实例。
// 它接收需要监控的对象列表，而不是任何管理器或系统对象，以实现解耦。
func NewDataCollector(
	wg *sync.WaitGroup,
	done <-chan struct{},
	aircrafts []*simulation.Aircraft,
	channels []*simulation.Channel, // 直接接收信道列表
	groundStations []*simulation.GroundControlCenter,
) *DataCollector {
	// 创建带有时间戳的唯一文件名
	baseFilename := fmt.Sprintf("simulation_report_%s.xlsx", time.Now().Format("20060102_150405"))
	fullPath := filepath.Join("report", baseFilename)

	return &DataCollector{
		aircrafts:      aircrafts,
		channels:       channels,
		groundStations: groundStations,
		filename:       fullPath,
		wg:             wg,
		done:           done,
		startTime:      time.Now(),
	}
}

// Run 启动数据收集过程。它应该在一个单独的goroutine中运行。
func (dc *DataCollector) Run() {
	defer dc.wg.Done()
	log.Printf("📊 独立数据收集器已启动，将每隔 %v 记录一次快照...", collectionInterval)

	f := excelize.NewFile()
	defer func() {
		if err := f.Close(); err != nil {
			log.Printf("❌ 关闭Excel文件时出错: %v", err)
		}
	}()

	// 为不同类型的数据创建工作表
	aircraftSheet, channelSheet, groundSheet := "Aircraft_Stats", "Channel_Stats", "GroundControl_Stats"
	f.NewSheet(aircraftSheet)
	f.NewSheet(channelSheet)
	f.NewSheet(groundSheet)
	f.DeleteSheet("Sheet1") // 删除默认创建的Sheet1

	// --- 写入所有工作表的表头 ---
	dc.writeHeaders(f, aircraftSheet, channelSheet, groundSheet)

	// 初始化行计数器
	aircraftRow, channelRow, groundRow := 2, 2, 2

	ticker := time.NewTicker(collectionInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// --- 定时记录数据快照 ---
			simMinutes := int(time.Since(dc.startTime).Minutes())
			log.Printf("📊 正在记录模拟时间 %d 分钟时的数据快照...", simMinutes)

			// 记录所有飞机的数据
			aircraftRow = dc.recordAircraftStats(f, aircraftSheet, aircraftRow, simMinutes)
			// 记录所有信道的数据
			channelRow = dc.recordChannelStats(f, channelSheet, channelRow, simMinutes)
			// 记录所有地面站的数据
			groundRow = dc.recordGroundStationStats(f, groundSheet, groundRow, simMinutes)

		case <-dc.done:

			simMinutes := int(time.Since(dc.startTime).Minutes())
			aircraftRow = dc.recordAircraftStats(f, aircraftSheet, aircraftRow, simMinutes)
			// 记录所有信道的数据
			channelRow = dc.recordChannelStats(f, channelSheet, channelRow, simMinutes)
			// 记录所有地面站的数据
			groundRow = dc.recordGroundStationStats(f, groundSheet, groundRow, simMinutes)

			// --- 接收到停止信号，执行最终保存 ---
			log.Println("✅ 模拟结束，正在整理并保存所有数据到Excel文件...")
			dc.saveReport(f)
			return // 结束 goroutine
		}
	}
}

// writeHeaders 负责向Excel文件写入表头。
func (dc *DataCollector) writeHeaders(f *excelize.File, aircraftSheet, channelSheet, groundSheet string) {
	headersAircraft := []string{"SimTime (min)", "航班号", "成功传输", "重传", "尝试传输", "碰撞次数", "碰撞率 (%)",
		"平均等待时间 (ms)", "请求信道", "失败请求信道", "请求信道失败率 (%)"}
	_ = f.SetSheetRow(aircraftSheet, "A1", &headersAircraft)

	headersChannel := []string{"SimTime (min)", "信道", "是否启用", "成功传输", "信道使用时间 (ms)", "信道使用率 (%)"}
	_ = f.SetSheetRow(channelSheet, "A1", &headersChannel)

	headersGround := []string{"SimTime (min)", "地面站名", "成功传输", "尝试传输", "碰撞次数", "碰撞率 (%)",
		"平均等待时间 (ms)", "请求信道", "失败请求信道", "请求信道失败率 (%)"}
	_ = f.SetSheetRow(groundSheet, "A1", &headersGround)
}

// recordAircraftStats 记录所有飞机的统计数据。
func (dc *DataCollector) recordAircraftStats(f *excelize.File, sheet string, startRow int, simMinutes int) int {
	row := startRow
	for _, ac := range dc.aircrafts {
		stats := ac.GetRawStats() // 调用接口获取原始数据
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
			simMinutes, ac.CurrentFlightID, stats.SuccessfulTx, stats.TotalRetries, stats.TotalTxAttempts, stats.TotalCollisions, collisionRate,
			avgWaitTimeMs, stats.TotalRqTunnel, stats.TotalFailRqTunnel, rqFailRate,
		}
		_ = f.SetSheetRow(sheet, fmt.Sprintf("A%d", row), &rowData)
		row++
	}
	return row
}

// recordChannelStats 记录所有信道的统计数据。
func (dc *DataCollector) recordChannelStats(f *excelize.File, sheet string, startRow int, simMinutes int) int {
	row := startRow
	totalSimDuration := time.Since(dc.startTime)

	for _, ch := range dc.channels {
		// 核心要求：即使信道未启用(nil)，也要忠实记录其状态
		if ch == nil {
			rowData := []interface{}{simMinutes, "Backup (Disabled)", "Disabled", 0, 0, 0.0}
			_ = f.SetSheetRow(sheet, fmt.Sprintf("A%d", row), &rowData)
			row++
			continue
		}

		stats := ch.GetRawStats() // 调用接口获取原始数据
		var utilization float64
		if totalSimDuration > 0 {
			utilization = (float64(stats.TotalBusyTime) / float64(totalSimDuration)) * 100
		}

		rowData := []interface{}{
			simMinutes, ch.ID, "Enabled", stats.TotalMessagesTransmitted, stats.TotalBusyTime.Milliseconds(), utilization,
		}
		_ = f.SetSheetRow(sheet, fmt.Sprintf("A%d", row), &rowData)
		row++
	}
	return row
}

// recordGroundStationStats 记录所有地面站的统计数据。
func (dc *DataCollector) recordGroundStationStats(f *excelize.File, sheet string, startRow int, simMinutes int) int {
	row := startRow
	for _, gcc := range dc.groundStations {
		stats := gcc.GetRawStats() // 调用接口获取原始数据
		var collisionRate float64
		if stats.TotalTxAttempts > 0 {
			collisionRate = (float64(stats.TotalCollisions) / float64(stats.TotalTxAttempts)) * 100
		}
		var avgWaitTimeMs float64
		if stats.SuccessfulTx > 0 {
			avgWaitTimeMs = float64(stats.TotalWaitTimeNs.Milliseconds()) / float64(stats.SuccessfulTx)
		}
		var rqFailRate float64
		if stats.TotalRqTunnel > 0 {
			rqFailRate = (float64(stats.TotalFailRqTunnel) / float64(stats.TotalRqTunnel)) * 100
		}

		rowData := []interface{}{
			simMinutes, gcc.ID, stats.SuccessfulTx, stats.TotalTxAttempts, stats.TotalCollisions, collisionRate,
			avgWaitTimeMs, stats.TotalRqTunnel, stats.TotalFailRqTunnel, rqFailRate,
		}
		_ = f.SetSheetRow(sheet, fmt.Sprintf("A%d", row), &rowData)
		row++
	}
	return row
}

// saveReport 负责创建目录并保存最终的Excel文件。
func (dc *DataCollector) saveReport(f *excelize.File) {
	// 在保存文件之前，确保目标目录存在
	reportDir := filepath.Dir(dc.filename)
	if err := os.MkdirAll(reportDir, 0755); err != nil {
		log.Printf("❌ 错误: 无法创建报告目录 '%s': %v", reportDir, err)
		return
	}

	// 保存文件
	if err := f.SaveAs(dc.filename); err != nil {
		log.Printf("❌ 错误: 无法保存 Excel 报告文件: %v", err)
	} else {
		log.Printf("✅ 模拟数据报告已成功保存到: %s", dc.filename)
	}
}
