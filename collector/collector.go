// C:/workspace/go/Air-Simulator-Go/collector/collector.go
package collector

import (
	"Air-Simulator/simulation" // 导入我们新的 simulation 包
	"fmt"
	"log"
	"os"            // 导入 os 包用于文件系统操作
	"path/filepath" // 导入 path/filepath 包用于处理文件路径
	"strconv"
	"sync"
	"time"

	"github.com/xuri/excelize/v2"
)

const (
	// collectionInterval 定义了数据收集和写入Excel的时间间隔。
	collectionInterval = 5 * time.Minute
)

// DataCollector 结构体封装了数据收集器的所有依赖和状态。
type DataCollector struct {
	aircraftList  []*simulation.Aircraft
	groundControl *simulation.GroundControlCenter
	commsChannel  *simulation.Channel
	filename      string
	wg            *sync.WaitGroup
	done          <-chan struct{}
}

// NewDataCollector 创建一个新的数据收集器实例。
func NewDataCollector(
	wg *sync.WaitGroup,
	done <-chan struct{},
	aircraftList []*simulation.Aircraft,
	groundControl *simulation.GroundControlCenter,
	commsChannel *simulation.Channel,
) *DataCollector {
	// 1. 创建一个带时间戳的基础文件名
	baseFilename := fmt.Sprintf("simulation_results_%s.xlsx", time.Now().Format("20060102_150405"))

	// 2. 使用 filepath.Join 将 "report" 目录和基础文件名安全地拼接成完整路径
	//    这样做可以跨平台兼容 (Windows, macOS, Linux)
	fullPath := filepath.Join("report", baseFilename)

	return &DataCollector{
		aircraftList:  aircraftList,
		groundControl: groundControl,
		commsChannel:  commsChannel,
		filename:      fullPath, // 使用包含目录的完整路径
		wg:            wg,
		done:          done,
	}
}

// Run 启动数据收集过程。它应该在一个单独的goroutine中运行。
func (dc *DataCollector) Run() {
	defer dc.wg.Done()
	log.Printf("📊 数据收集器已启动，将每隔 %v 记录一次数据...", collectionInterval)

	f := excelize.NewFile()
	defer func() {
		if err := f.Close(); err != nil {
			log.Printf("❌ 关闭Excel文件时出错: %v", err)
		}
	}()

	aircraftSheet, groundSheet, channelSheet := "Aircraft_Stats", "GroundControl_Stats", "Channel_Stats"
	f.NewSheet(aircraftSheet)
	f.NewSheet(groundSheet)
	f.NewSheet(channelSheet)
	f.DeleteSheet("Sheet1")

	// --- 写入表头 (已更新) ---
	headersAircraft := []string{"时间 (Sim Minutes)", "飞机号", "成功传输", "尝试传输", "碰撞次数", "重传次数", "碰撞率 (%)", "平均等待时间 (ms)", "请求通道数", "失败请求通道数", "请求通道失败率"}
	_ = f.SetSheetRow(aircraftSheet, "A1", &headersAircraft)
	aircraftRow := 2

	headersGround := []string{"时间 (Sim Minutes)", "地面站", "成功传输", "尝试传输", "碰撞次数", "碰撞率 (%)", "平均等待时间 (ms)", "请求通道数", "失败请求通道数", "请求通道失败率"}
	_ = f.SetSheetRow(groundSheet, "A1", &headersGround)
	groundRow := 2

	headersChannel := []string{"时间 (Sim Minutes)", "总共传输报文", "信道总占用时间 (ms)"}
	_ = f.SetSheetRow(channelSheet, "A1", &headersChannel)
	channelRow := 2

	ticker := time.NewTicker(collectionInterval)
	defer ticker.Stop()

	simMinutes := 0

	for {
		select {
		case <-ticker.C:
			simMinutes += int(collectionInterval.Minutes())
			timestampStr := strconv.Itoa(simMinutes)
			log.Printf("📊 数据已在模拟时间 %d 分钟时记录...", simMinutes)

			// --- 收集并写入所有飞机的数据 (已更新) ---
			for _, ac := range dc.aircraftList {
				stats := ac.GetRawStats()
				var collisionRate float64
				// 计算碰撞率，并处理分母为0的情况
				if stats.TotalTxAttempts > 0 {
					collisionRate = (float64(stats.TotalCollisions) / float64(stats.TotalTxAttempts)) * 100
				}

				var avgWaitTimeMs float64
				if stats.SuccessfulTx > 0 {
					// 平均等待时间 = 总等待时间 / 成功发送的报文数
					avgWaitTimeMs = float64(stats.TotalWaitTime.Milliseconds()) / float64(stats.SuccessfulTx+stats.TotalRetries)
				}

				var rqTunnelRate float64
				if stats.TotalRqTunnel > 0 {
					rqTunnelRate = (float64(stats.TotalFailRqTunnel) / float64(stats.TotalRqTunnel)) * 100
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
					rqTunnelRate,
				}
				_ = f.SetSheetRow(aircraftSheet, fmt.Sprintf("A%d", aircraftRow), &rowData)
				aircraftRow++
			}

			// --- 收集并写入地面站数据 (已更新) ---
			gcStats := dc.groundControl.GetRawStats()
			var gcCollisionRate float64
			// 计算碰撞率，并处理分母为0的情况
			if gcStats.TotalTxAttempts > 0 {
				gcCollisionRate = (float64(gcStats.TotalCollisions) / float64(gcStats.TotalTxAttempts)) * 100
			}

			var gcAvgWaitTimeMs float64
			if gcStats.SuccessfulTx > 0 {
				// 平均等待时间 = 总等待时间 / 成功发送的报文数
				gcAvgWaitTimeMs = float64(gcStats.TotalWaitTimeNs.Milliseconds()) / float64(gcStats.SuccessfulTx)
			}

			var rqTunnelRate float64
			if gcStats.TotalRqTunnel > 0 {
				rqTunnelRate = (float64(gcStats.TotalFailRqTunnel) / float64(gcStats.TotalRqTunnel)) * 100
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
				rqTunnelRate,
			}
			_ = f.SetSheetRow(groundSheet, fmt.Sprintf("A%d", groundRow), &gcRowData)
			groundRow++

			// 收集并写入信道数据
			chStats := dc.commsChannel.GetRawStats()
			chRowData := []interface{}{timestampStr, chStats.TotalMessagesTransmitted, chStats.TotalBusyTime.Milliseconds()}
			_ = f.SetSheetRow(channelSheet, fmt.Sprintf("A%d", channelRow), &chRowData)
			channelRow++

		case <-dc.done:
			// 3. 在保存文件之前，确保目标目录存在
			//    首先从完整文件名中提取目录部分
			reportDir := filepath.Dir(dc.filename)
			//    然后使用 os.MkdirAll 创建目录。这个函数是安全的，如果目录已存在，它不会做任何事也不会报错。
			if err := os.MkdirAll(reportDir, 0755); err != nil {
				log.Printf("❌ 错误: 无法创建报告目录 '%s': %v", reportDir, err)
				// 即使创建目录失败，也尝试保存，以防根目录可写
			}

			if err := f.SaveAs(dc.filename); err != nil {
				log.Printf("❌ 错误: 无法保存 Excel 文件: %v", err)
			} else {
				log.Printf("✅ 模拟数据已成功保存到 %s", dc.filename)
			}
			return
		}
	}
}
