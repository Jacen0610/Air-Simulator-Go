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

	// --- [新增] 用于收集原始 waitTime 指标 ---
	allWaitTimes []time.Duration
	metricsChan  <-chan time.Duration // 接收指标的通道 (只读)
	metricsWg    sync.WaitGroup       // 用于等待指标收集goroutine
}

// NewDataCollector 创建一个新的数据收集器实例。
// [修改] 增加 metricsChan 参数
func NewDataCollector(
	wg *sync.WaitGroup,
	done <-chan struct{},
	aircrafts []*simulation.Aircraft,
	channels []*simulation.Channel, // 直接接收信道列表
	groundStations []*simulation.GroundControlCenter,
	metricsChan <-chan time.Duration, // [新增]
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
		// [新增] 初始化指标收集相关字段
		allWaitTimes: make([]time.Duration, 0, 20000), // 预分配一些容量
		metricsChan:  metricsChan,
	}
}

// Run 启动数据收集过程。它应该在一个单独的goroutine中运行。
func (dc *DataCollector) Run() {
	defer dc.wg.Done()
	log.Printf("📊 独立数据收集器已启动，将每隔 %v 记录一次快照...", collectionInterval)

	// [新增] 启动一个goroutine专门从metricsChan收集数据
	dc.metricsWg.Add(1)
	go dc.collectWaitTimes()

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
			// --- [修改] 接收到停止信号，执行最终的整理和保存 ---
			log.Println("📊 模拟主程序已结束，等待指标通道关闭...")
			// main函数会先关闭metricsChan，然后关闭doneChan。
			// 这里需要等待collectWaitTimes goroutine处理完所有数据。
			dc.metricsWg.Wait()
			log.Printf("... 指标通道已处理完毕，共收集到 %d 条原始 WaitTime 数据。", len(dc.allWaitTimes))

			// 记录最终的快照
			simMinutes := int(time.Since(dc.startTime).Minutes())
			dc.recordAircraftStats(f, aircraftSheet, aircraftRow, simMinutes)
			dc.recordChannelStats(f, channelSheet, channelRow, simMinutes)
			dc.recordGroundStationStats(f, groundSheet, groundRow, simMinutes)

			// [新增] 记录 WaitTime 分布
			dc.recordWaitTimeDistribution(f)

			log.Println("✅ 正在整理并保存所有数据到Excel文件...")
			dc.saveReport(f)
			return // 结束 goroutine
		}
	}
}

// [新增] collectWaitTimes 从指标通道读取数据并存入切片。
func (dc *DataCollector) collectWaitTimes() {
	defer dc.metricsWg.Done()
	for wt := range dc.metricsChan {
		dc.allWaitTimes = append(dc.allWaitTimes, wt)
	}
}

// [新增] recordWaitTimeDistribution 创建一个新工作表并写入所有原始的waitTime数据。
func (dc *DataCollector) recordWaitTimeDistribution(f *excelize.File) {
	sheet := "WaitTime_Distribution"
	f.NewSheet(sheet)
	log.Printf("📊 正在将 %d 条 WaitTime 分布数据写入 %s 工作表...", len(dc.allWaitTimes), sheet)

	// 写入表头
	headers := []string{"WaitTime (ms)"}
	_ = f.SetSheetRow(sheet, "A1", &headers)

	// 写入数据
	for i, wt := range dc.allWaitTimes {
		// excelize 期望的是 interface{}，我们直接传入 float64
		// i+2 是因为Excel行号从1开始，且第1行是表头
		_ = f.SetCellValue(sheet, fmt.Sprintf("A%d", i+2), float64(wt.Nanoseconds())/1e6)
	}
}

// writeHeaders 负责向Excel文件写入表头。
func (dc *DataCollector) writeHeaders(f *excelize.File, aircraftSheet, channelSheet, groundSheet string) {
	headersAircraft := []string{"SimTime (min)", "航班号", "成功传输", "重传", "丢弃消息数", "尝试传输", "碰撞次数", "碰撞率 (%)",
		"平均等待时间 (ms)", "请求信道", "失败请求信道", "请求信道失败率 (%)"}
	_ = f.SetSheetRow(aircraftSheet, "A1", &headersAircraft)

	headersChannel := []string{"SimTime (min)", "信道", "是否启用", "成功传输", "信道使用时间 (ms)", "信道使用率 (%)"}
	_ = f.SetSheetRow(channelSheet, "A1", &headersChannel)

	headersGround := []string{"SimTime (min)", "地面站名", "成功传输", "丢弃消息数", "尝试传输", "碰撞次数", "碰撞率 (%)",
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
			simMinutes, ac.CurrentFlightID, stats.SuccessfulTx, stats.TotalRetries, stats.TotalDroppedMessages, stats.TotalTxAttempts, stats.TotalCollisions, collisionRate,
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
			simMinutes, gcc.ID, stats.SuccessfulTx, stats.TotalDroppedMessages, stats.TotalTxAttempts, stats.TotalCollisions, collisionRate,
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
