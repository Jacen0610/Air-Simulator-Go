package main

import (
	"fmt"
	"time"
)

// Aircraft 结构体定义了一架航空器的所有关键参数
type Aircraft struct {
	// --- 识别与注册信息 ---
	ICAOAddress  string `json:"icaoAddress"`  // ICAO 24 位地址，全球唯一
	Registration string `json:"registration"` // 注册号 / 机号 (例如: B-6001)
	AircraftType string `json:"aircraftType"` // 飞机型号 (例如: B737-800)
	Manufacturer string `json:"manufacturer"` // 制造商 (例如: Boeing)
	SerialNumber string `json:"serialNumber"` // 制造商序列号

	// --- 运营与归属信息 ---
	AirlineICAOCode    string          `json:"airlineICAOCode"`          // 所属航空公司 ICAO 代码 (例如: CCA)
	CurrentFlightID    string          `json:"currentFlightID"`          // 当前执飞航班号 (例如: CCA1234)
	CurrentFlightPhase string          `json:"currentFlightPhase"`       // 当前飞行阶段
	LastOOOIReport     *OOOIReportData `json:"lastOOOIReport,omitempty"` // 最新的 OOOI 报告，使用指针表示可能为空

	// --- 位置与状态信息 ---
	CurrentPosition         *PositionReportData       `json:"currentPosition,omitempty"` // 当前位置，使用指针表示可能为空
	FuelRemainingKG         float64                   `json:"fuelRemainingKG"`           // 剩余燃油量 (公斤)
	FuelConsumptionRateKGPH float64                   `json:"fuelConsumptionRateKGPH"`   // 实时燃油消耗率 (公斤/小时)
	EngineStatus            map[int]*EngineReportData `json:"engineStatus,omitempty"`    // 各个发动机的最新状态，键为发动机编号
	LastDataReportTimestamp time.Time                 `json:"lastDataReportTimestamp"`   // 最新状态数据报告时间
	SquawkCode              string                    `json:"squawkCode"`                // 应答机代码 (Transponder Code)

	// --- 通信与系统能力 ---
	ACARSEnabled          bool   `json:"acarsEnabled"`          // 是否启用 ACARS 功能
	CPDLCEnabled          bool   `json:"cpdlcEnabled"`          // 是否启用 CPDLC 功能
	SatelliteCommsEnabled bool   `json:"satelliteCommsEnabled"` // 是否启用卫星通信
	SoftwareVersion       string `json:"softwareVersion"`       // 机载系统软件版本
}

// NewAircraft 创建一个航空器实例的构造函数
func NewAircraft(icaoAddr, reg, aircraftType, manufacturer, serialNum, airlineCode string) *Aircraft {
	return &Aircraft{
		ICAOAddress:             icaoAddr,
		Registration:            reg,
		AircraftType:            aircraftType,
		Manufacturer:            manufacturer,
		SerialNumber:            serialNum,
		AirlineICAOCode:         airlineCode,
		EngineStatus:            make(map[int]*EngineReportData), // 初始化 Map
		LastDataReportTimestamp: time.Now(),                      // 初始时间
	}
}

// UpdatePosition 更新航空器的位置信息
func (a *Aircraft) UpdatePosition(lat, lon, alt, speed, heading float64) {
	a.CurrentPosition = &PositionReportData{
		Latitude:  lat,
		Longitude: lon,
		Altitude:  alt,
		Speed:     speed,
		Heading:   heading,
		Timestamp: time.Now(),
	}
	a.LastDataReportTimestamp = time.Now()
}

// UpdateFuel 更新航空器的燃油信息
func (a *Aircraft) UpdateFuel(remainingKG, consumptionRateKGPH float64) {
	a.FuelRemainingKG = remainingKG
	a.FuelConsumptionRateKGPH = consumptionRateKGPH
	a.LastDataReportTimestamp = time.Now()
}

// UpdateEngineStatus 更新特定发动机的状态
func (a *Aircraft) UpdateEngineStatus(engineID int, n1, egt, fuelFlow, oilPressure float64, flightPhase string) {
	a.EngineStatus[engineID] = &EngineReportData{
		EngineID:      engineID,
		N1RPM:         n1,
		EGT:           egt,
		FuelFlow:      fuelFlow,
		OilPressure:   oilPressure,
		FlightPhase:   flightPhase,
		ReportTimeUTC: time.Now().UTC(),
	}
	a.LastDataReportTimestamp = time.Now()
}

// UpdateOOOIReport 更新 OOOI 报告
func (a *Aircraft) UpdateOOOIReport(out, off, on, in time.Time, origin, dest string) {
	a.LastOOOIReport = &OOOIReportData{
		OutTime: out,
		OffTime: off,
		OnTime:  on,
		InTime:  in,
		Origin:  origin,
		Dest:    dest,
	}
	a.LastDataReportTimestamp = time.Now()
}

// GetInfo 打印航空器简要信息
func (a *Aircraft) GetInfo() string {
	info := fmt.Sprintf("飞机 %s (%s) - %s %s\n", a.Registration, a.ICAOAddress, a.Manufacturer, a.AircraftType)
	info += fmt.Sprintf("  当前航班: %s, 飞行阶段: %s\n", a.CurrentFlightID, a.CurrentFlightPhase)
	if a.CurrentPosition != nil {
		info += fmt.Sprintf("  当前位置: 纬度 %.4f, 经度 %.4f, 高度 %.0fft, 速度 %.0fkt\n",
			a.CurrentPosition.Latitude, a.CurrentPosition.Longitude, a.CurrentPosition.Altitude, a.CurrentPosition.Speed)
	}
	info += fmt.Sprintf("  剩余燃油: %.2f KG, 消耗率: %.2f KG/H\n", a.FuelRemainingKG, a.FuelConsumptionRateKGPH)
	info += fmt.Sprintf("  ACARS Enabled: %t, CPDLC Enabled: %t\n", a.ACARSEnabled, a.CPDLCEnabled)
	return info
}
