// C:/workspace/go/Air-Simulator-Go/collector/collector.go
package collector

import (
	"Air-Simulator/protos" // 新增: 导入 protos 包以访问 Action 结构体
	"Air-Simulator/simulation"
	"fmt"
	"github.com/xuri/excelize/v2"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// DataCollector 结构体现在是会话报告管理器，持有报告生成所需的所有状态。
type DataCollector struct {
	// 依赖的仿真组件
	aircraftList  []*simulation.Aircraft
	groundControl *simulation.GroundControlCenter
	commSystem    *simulation.CommunicationSystem

	// Excel 文件状态
	excelFile         *excelize.File
	aircraftRow       int
	groundRow         int
	primaryChannelRow int
	backupChannelRow  int
	actionRow         int // 新增: 用于追踪 Agent 决策的行号
}

// NewDataCollector 创建一个新的数据收集器实例。
func NewDataCollector(
	aircraftList []*simulation.Aircraft,
	groundControl *simulation.GroundControlCenter,
	commSystem *simulation.CommunicationSystem,
) *DataCollector {
	return &DataCollector{
		aircraftList:  aircraftList,
		groundControl: groundControl,
		commSystem:    commSystem,
	}
}

// InitializeEpisode 在每个新的模拟会话开始时调用，用于创建和准备Excel文件。
func (dc *DataCollector) InitializeEpisode() {
	log.Println("📊 Collector: Initializing new report for the episode...")
	dc.excelFile = excelize.NewFile()

	// 创建工作表
	aircraftSheet := "Aircraft_Periodic_Stats"
	groundSheet := "GroundControl_Periodic_Stats"
	primaryChannelSheet := "Channel_Primary_Periodic_Stats"
	actionSheet := "Agent_Actions" // 新增: Agent 决策工作表
	dc.excelFile.NewSheet(aircraftSheet)
	dc.excelFile.NewSheet(groundSheet)
	dc.excelFile.NewSheet(primaryChannelSheet)
	dc.excelFile.NewSheet(actionSheet) // 新增

	var backupChannelSheet string
	if dc.commSystem.BackupChannel != nil {
		backupChannelSheet = "Channel_Backup_Periodic_Stats"
		dc.excelFile.NewSheet(backupChannelSheet)
	}
	dc.excelFile.DeleteSheet("Sheet1")

	// 写入表头
	headersAircraft := []string{"时间 (Sim Minutes)", "飞机号", "成功传输", "尝试传输", "碰撞次数", "重传次数", "碰撞率 (%)", "平均等待时间 (ms)", "请求通道数", "失败请求通道数", "请求通道失败率"}
	_ = dc.excelFile.SetSheetRow(aircraftSheet, "A1", &headersAircraft)

	headersGround := []string{"时间 (Sim Minutes)", "地面站", "成功传输", "尝试传输", "碰撞次数", "碰撞率 (%)", "平均等待时间 (ms)", "请求通道数", "失败请求通道数", "请求通道失败率"}
	_ = dc.excelFile.SetSheetRow(groundSheet, "A1", &headersGround)

	headersChannel := []string{"时间 (Sim Minutes)", "总共传输报文", "信道总占用时间 (ms)"}
	_ = dc.excelFile.SetSheetRow(primaryChannelSheet, "A1", &headersChannel)
	if backupChannelSheet != "" {
		_ = dc.excelFile.SetSheetRow(backupChannelSheet, "A1", &headersChannel)
	}

	// 新增: 写入 Agent 决策的表头
	headersActions := []string{"时间 (Sim Minutes)", "P_Critical", "P_High", "P_Medium", "P_Low", "TimeSlot (ms)"}
	_ = dc.excelFile.SetSheetRow(actionSheet, "A1", &headersActions)

	// 初始化行计数器
	dc.aircraftRow = 2
	dc.groundRow = 2
	dc.primaryChannelRow = 2
	dc.backupChannelRow = 2
	dc.actionRow = 2 // 新增
}

// CollectActionData 记录 Agent 在每个时间步做出的决策。
func (dc *DataCollector) CollectActionData(simMinutes int, action *protos.Action) {
	if dc.excelFile == nil {
		log.Println("❌ Collector Error: CollectActionData called before InitializeEpisode.")
		return
	}
	if action == nil {
		log.Println("❌ Collector Error: CollectActionData called with a nil action.")
		return
	}

	timestampStr := strconv.Itoa(simMinutes)
	actionSheet := "Agent_Actions"

	rowData := []interface{}{
		timestampStr,
		action.PCritical,
		action.PHigh,
		action.PMedium,
		action.PLow,
		action.TimeSlotMs,
	}

	// 使用 fmt.Sprintf 构造单元格地址，与其他部分保持一致
	cell := fmt.Sprintf("A%d", dc.actionRow)
	if err := dc.excelFile.SetSheetRow(actionSheet, cell, &rowData); err != nil {
		log.Printf("❌ 错误: 无法向 %s 工作表写入 Agent 决策数据: %v", actionSheet, err)
	}
	dc.actionRow++
}

// CollectPeriodicData 在每个5分钟的时间点被调用，用于记录当前系统状态的快照。
func (dc *DataCollector) CollectPeriodicData(simMinutes int) {
	if dc.excelFile == nil {
		log.Println("❌ Collector Error: CollectPeriodicData called before InitializeEpisode.")
		return
	}
	log.Printf("📊 Collector: Collecting periodic data at simulation time %d minutes...", simMinutes)
	timestampStr := strconv.Itoa(simMinutes)

	// --- 收集并写入所有飞机的当前数据 ---
	for _, ac := range dc.aircraftList {
		stats := ac.GetRawStats()
		var collisionRate float64
		if stats.TotalTxAttempts > 0 {
			collisionRate = (float64(stats.TotalCollisions) / float64(stats.TotalTxAttempts)) * 100
		}
		var avgWaitTimeMs float64
		if stats.SuccessfulTx > 0 {
			avgWaitTimeMs = float64(stats.TotalWaitTime.Milliseconds()) / float64(stats.SuccessfulTx)
		}
		var rqFailRate float64
		if stats.TotalRqTunnel > 0 {
			rqFailRate = (float64(stats.TotalFailRqTunnel) / float64(stats.TotalRqTunnel)) * 100
		}
		rowData := []interface{}{
			timestampStr,
			ac.CurrentFlightID,
			stats.SuccessfulTx,
			stats.TotalTxAttempts,
			stats.TotalCollisions,
			stats.TotalRetries,
			collisionRate,
			avgWaitTimeMs,
			stats.TotalRqTunnel,
			stats.TotalFailRqTunnel,
			rqFailRate,
		}
		_ = dc.excelFile.SetSheetRow("Aircraft_Periodic_Stats", fmt.Sprintf("A%d", dc.aircraftRow), &rowData)
		dc.aircraftRow++
	}

	// --- 收集并写入地面站的当前数据 ---
	gcStats := dc.groundControl.GetRawStats()
	var gcCollisionRate float64
	if gcStats.TotalTxAttempts > 0 {
		gcCollisionRate = (float64(gcStats.TotalCollisions) / float64(gcStats.TotalTxAttempts)) * 100
	}
	var gcAvgWaitTimeMs float64
	if gcStats.SuccessfulTx > 0 {
		gcAvgWaitTimeMs = float64(gcStats.TotalWaitTimeNs.Milliseconds()) / float64(gcStats.SuccessfulTx)
	}
	var gcRqFailRate float64
	if gcStats.TotalRqTunnel > 0 {
		gcRqFailRate = (float64(gcStats.TotalFailRqTunnel) / float64(gcStats.TotalRqTunnel)) * 100
	}
	gcRowData := []interface{}{
		timestampStr,
		dc.groundControl.ID,
		gcStats.SuccessfulTx,
		gcStats.TotalTxAttempts,
		gcStats.TotalCollisions,
		gcCollisionRate,
		gcAvgWaitTimeMs,
		gcStats.TotalRqTunnel,
		gcStats.TotalFailRqTunnel,
		gcRqFailRate,
	}
	_ = dc.excelFile.SetSheetRow("GroundControl_Periodic_Stats", fmt.Sprintf("A%d", dc.groundRow), &gcRowData)
	dc.groundRow++

	// --- 收集并写入信道的当前数据 ---
	primaryChStats := dc.commSystem.PrimaryChannel.GetRawStats()
	primaryChRowData := []interface{}{timestampStr, primaryChStats.TotalMessagesTransmitted, primaryChStats.TotalBusyTime.Milliseconds()}
	_ = dc.excelFile.SetSheetRow("Channel_Primary_Periodic_Stats", fmt.Sprintf("A%d", dc.primaryChannelRow), &primaryChRowData)
	dc.primaryChannelRow++

	if dc.commSystem.BackupChannel != nil {
		backupChStats := dc.commSystem.BackupChannel.GetRawStats()
		backupChRowData := []interface{}{timestampStr, backupChStats.TotalMessagesTransmitted, backupChStats.TotalBusyTime.Milliseconds()}
		_ = dc.excelFile.SetSheetRow("Channel_Backup_Periodic_Stats", fmt.Sprintf("A%d", dc.backupChannelRow), &backupChRowData)
		dc.backupChannelRow++
	}
}

// SaveFinalReport 在模拟会话结束后调用，将内存中的Excel文件保存到磁盘。
func (dc *DataCollector) SaveFinalReport() {
	if dc.excelFile == nil {
		log.Println("❌ Collector Error: SaveFinalReport called before InitializeEpisode.")
		return
	}
	log.Println("💾 Collector: Saving final report...")

	filename := fmt.Sprintf("simulation_report_%s.xlsx", time.Now().Format("20060102_150405"))
	fullPath := filepath.Join("report", filename)

	// 确保报告目录存在
	reportDir := filepath.Dir(fullPath)
	if err := os.MkdirAll(reportDir, 0755); err != nil {
		log.Printf("❌ 错误: 无法创建报告目录 '%s': %v", reportDir, err)
	}

	// 保存文件
	if err := dc.excelFile.SaveAs(fullPath); err != nil {
		log.Printf("❌ 错误: 无法保存 Excel 文件: %v", err)
	} else {
		log.Printf("✅ 最终统计报告已成功保存到 %s", fullPath)
	}

	// 关闭文件句柄
	if err := dc.excelFile.Close(); err != nil {
		log.Printf("❌ 关闭Excel文件时出错: %v", err)
	}
	dc.excelFile = nil // 清理引用
}
