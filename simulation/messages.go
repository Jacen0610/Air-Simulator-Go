package simulation

import (
	"encoding/json"
	"time"
)

type Priority string

const (
	HighPriority     Priority = "HIGH"
	CriticalPriority Priority = "CRITICAL"
	LowPriority      Priority = "LOW"
	MediumPriority   Priority = "Medium"
)

// MessageType 定义了 ACARS 报文的类型，便于识别
type MessageType string

const (
	MsgTypeAircraftFault MessageType = "AIRCRAFT_FAULT" // 飞机系统故障报告
	MsgTypeATCMessage    MessageType = "ATC_MESSAGE"    // 空中交通管制消息

	MsgTypeOOOI     MessageType = "OOOI_REPORT"     // Out, Off, On, In Report
	MsgTypePosition MessageType = "POSITION_REPORT" // 位置报告
	MsgTypeFuel     MessageType = "FUEL_REPORT"     // 燃油报告

	MsgTypeEngineReport MessageType = "ENGINE_REPORT"  // 发动机性能报告
	MsgTypeWeather      MessageType = "WEATHER_REPORT" // 天气请求/报告
	MsgTypePDC          MessageType = "PDC"            // 预发离港许可
	MsgTypeDATIS        MessageType = "D_ATIS"         // 数字自动终端信息服务

	MsgTypeFreeText MessageType = "FREE_TEXT"       // 自由文本消息
	MsgTypeLinkTest MessageType = "LINK_TEST"       // ACARS 链路测试
	MsgTypeAck      MessageType = "ACKNOWLEDGEMENT" // 确认消息
)

// ACARSBaseMessage 包含了所有 ACARS 报文的通用头部信息
type ACARSBaseMessage struct {
	AircraftICAOAddress string      `json:"aircraftICAOAddress"` // 飞机ICAO地址 (例如: "A87654")
	FlightID            string      `json:"flightID"`            // 航班号 (例如: "CCA123")
	MessageID           string      `json:"messageID"`           // 唯一的报文ID
	Timestamp           time.Time   `json:"timestamp"`           // 报文发送时间
	Type                MessageType `json:"type"`                // 报文的具体类型
}

// ACARSMessageInterface 定义一个接口，用于统一处理所有优先级的 ACARS 消息
type ACARSMessageInterface interface {
	GetBaseMessage() ACARSBaseMessage
	GetData() interface{}
	GetPriority() Priority // 可以返回字符串表示的优先级
}

// AircraftFaultData 飞机系统故障数据
type AircraftFaultData struct {
	FaultCode   string    `json:"faultCode"`   // 故障代码 (例如: "ENG1-OVHT")
	Description string    `json:"description"` // 故障描述
	Severity    string    `json:"severity"`    // 严重性 (例如: "CRITICAL", "MAJOR")
	Timestamp   time.Time `json:"faultTime"`   // 故障发生时间
	System      string    `json:"system"`      // 发生故障的系统 (例如: "ENGINE_1", "HYDRAULIC")
}

// ATCMessageData 空中交通管制消息数据
type ATCMessageData struct {
	ATCMsgType string `json:"atcMsgType"` // ATC消息类型 (例如: "CLEARANCE", "REQUEST", "REPORT")
	Content    string `json:"content"`    // ATC消息的具体文本内容 (例如: "CLEARED DIRECT ABCDE")
	Reference  string `json:"reference"`  // 可能的参考信息 (例如: 许可编号)
}

type AcknowledgementData struct {
	OriginalMessageID string `json:"originalMessageID"` // 确认的是哪条原始报文的ID
	Status            string `json:"status"`            // 确认状态 (例如: "RECEIVED", "FAILED")
}

// CriticalHighPriorityMessage 封装了紧急/高优先级的 ACARS 报文
type CriticalHighPriorityMessage struct {
	ACARSBaseMessage
	Data json.RawMessage `json:"data"` // 存储具体的故障或ATC消息数据
}

// GetBaseMessage 实现 ACARSMessageInterface 接口
func (m CriticalHighPriorityMessage) GetBaseMessage() ACARSBaseMessage { return m.ACARSBaseMessage }

// GetData 实现 ACARSMessageInterface 接口
func (m CriticalHighPriorityMessage) GetData() interface{} { return m.Data }

// GetPriority 实现 ACARSMessageInterface 接口
func (m CriticalHighPriorityMessage) GetPriority() Priority { return CriticalPriority }

// 实例化一个高风险的Message
func NewCriticalHighPriorityMessage(base ACARSBaseMessage, data interface{}) (CriticalHighPriorityMessage, error) {
	rawData, err := json.Marshal(data)
	if err != nil {
		return CriticalHighPriorityMessage{}, err
	}
	return CriticalHighPriorityMessage{
		ACARSBaseMessage: base,
		Data:             rawData,
	}, nil
}

// OOOIReportData OOOI 报告数据
type OOOIReportData struct {
	OutTime time.Time `json:"outTime"` // 推出时间
	OffTime time.Time `json:"offTime"` // 离地时间
	OnTime  time.Time `json:"onTime"`  // 触地时间
	InTime  time.Time `json:"inTime"`  // 停靠时间
	Origin  string    `json:"origin"`  // 起始机场
	Dest    string    `json:"dest"`    // 目的机场
}

// PositionReportData 位置报告数据
type PositionReportData struct {
	Latitude  float64   `json:"latitude"`  // 纬度
	Longitude float64   `json:"longitude"` // 经度
	Altitude  float64   `json:"altitude"`  // 高度 (英尺)
	Speed     float64   `json:"speed"`     // 地速 (节)
	Heading   float64   `json:"heading"`   // 航向 (度)
	Timestamp time.Time `json:"posTime"`   // 报告时间
}

// FuelReportData 燃油报告数据
type FuelReportData struct {
	RemainingFuelKG float64   `json:"remainingFuelKG"` // 剩余燃油 (公斤)
	FuelFlowKGPH    float64   `json:"fuelFlowKGPH"`    // 当前燃油流量 (公斤/小时)
	EstimatedTime   time.Time `json:"estimatedTime"`   // 估计到达时间
}

// HighMediumPriorityMessage 封装了高/中优先级的 ACARS 报文
type HighMediumPriorityMessage struct {
	ACARSBaseMessage
	Data json.RawMessage `json:"data"` // 存储具体的 OOOI, 位置, 燃油数据
}

// GetBaseMessage 实现 ACARSMessageInterface 接口
func (m HighMediumPriorityMessage) GetBaseMessage() ACARSBaseMessage { return m.ACARSBaseMessage }

// GetData 实现 ACARSMessageInterface 接口
func (m HighMediumPriorityMessage) GetData() interface{} { return m.Data }

// GetPriority 实现 ACARSMessageInterface 接口
func (m HighMediumPriorityMessage) GetPriority() Priority { return HighPriority }

// Helper function to create HighMediumPriorityMessage
func NewHighMediumPriorityMessage(base ACARSBaseMessage, data interface{}) (HighMediumPriorityMessage, error) {
	rawData, err := json.Marshal(data)
	if err != nil {
		return HighMediumPriorityMessage{}, err
	}
	return HighMediumPriorityMessage{
		ACARSBaseMessage: base,
		Data:             rawData,
	}, nil
}

// EngineReportData 发动机性能报告数据
type EngineReportData struct {
	EngineID      int       `json:"engineID"`      // 发动机编号
	N1RPM         float64   `json:"n1RPM"`         // N1 转速百分比
	EGT           float64   `json:"egt"`           // 排气温度 (C)
	FuelFlow      float64   `json:"fuelFlow"`      // 燃油流量 (公斤/小时)
	OilPressure   float64   `json:"oilPressure"`   // 滑油压力
	FlightPhase   string    `json:"flightPhase"`   // 飞行阶段
	ReportTimeUTC time.Time `json:"reportTimeUTC"` // 报告UTC时间
}

// WeatherData 天气请求/报告数据
type WeatherData struct {
	RequestType string `json:"requestType"` // 请求类型 (例如: "METAR", "TAF", "WINDS ALOFT")
	Location    string `json:"location"`    // 机场ICAO代码或区域 (例如: "ZSSS")
	Content     string `json:"content"`     // 实际天气信息文本
}

// PDCData 预发离港许可数据
type PDCData struct {
	ClearanceID     string `json:"clearanceID"`     // 许可ID
	DepartureRunway string `json:"departureRunway"` // 离港跑道
	SID             string `json:"sid"`             // 标准仪表离场
	Squawk          string `json:"squawk"`          // 应答机代码
	Content         string `json:"content"`         // 完整许可文本
}

// DATISData 数字ATIS数据
type DATISData struct {
	AirportICAO string `json:"airportICAO"` // 机场ICAO代码
	Edition     string `json:"edition"`     // ATIS版本 (例如: "BRAVO", "CHARLIE")
	Content     string `json:"content"`     // ATIS完整文本
}

// MediumLowPriorityMessage 封装了中/低优先级的 ACARS 报文
type MediumLowPriorityMessage struct {
	ACARSBaseMessage
	Data json.RawMessage `json:"data"` // 存储具体的发动机, 天气, PDC, D-ATIS数据
}

// GetBaseMessage 实现 ACARSMessageInterface 接口
func (m MediumLowPriorityMessage) GetBaseMessage() ACARSBaseMessage { return m.ACARSBaseMessage }

// GetData 实现 ACARSMessageInterface 接口
func (m MediumLowPriorityMessage) GetData() interface{} { return m.Data }

// GetPriority 实现 ACARSMessageInterface 接口
func (m MediumLowPriorityMessage) GetPriority() Priority { return MediumPriority }

// Helper function to create MediumLowPriorityMessage
func NewMediumLowPriorityMessage(base ACARSBaseMessage, data interface{}) (MediumLowPriorityMessage, error) {
	rawData, err := json.Marshal(data)
	if err != nil {
		return MediumLowPriorityMessage{}, err
	}
	return MediumLowPriorityMessage{
		ACARSBaseMessage: base,
		Data:             rawData,
	}, nil
}

// FreeTextData 自由文本消息数据
type FreeTextData struct {
	Sender    string `json:"sender"`    // 发送方 (例如: "PILOT", "DISPATCH")
	Recipient string `json:"recipient"` // 接收方
	Content   string `json:"content"`   // 消息内容
}

// LinkTestData 链路测试数据 (通常无需具体内容，仅指示测试发生)
type LinkTestData struct {
	Result string `json:"result"` // 测试结果 (例如: "SUCCESS", "FAILURE")
}

// LowAuxiliaryPriorityMessage 封装了低优先级/辅助性的 ACARS 报文
type LowAuxiliaryPriorityMessage struct {
	ACARSBaseMessage
	Data json.RawMessage `json:"data"` // 存储具体的自由文本, 链路测试数据
}

// GetBaseMessage 实现 ACARSMessageInterface 接口
func (m LowAuxiliaryPriorityMessage) GetBaseMessage() ACARSBaseMessage { return m.ACARSBaseMessage }

// GetData 实现 ACARSMessageInterface 接口
func (m LowAuxiliaryPriorityMessage) GetData() interface{} { return m.Data }

// GetPriority 实现 ACARSMessageInterface 接口
func (m LowAuxiliaryPriorityMessage) GetPriority() Priority { return LowPriority }

// LowAuxiliaryPriorityMessage实例化函数
func NewLowAuxiliaryPriorityMessage(base ACARSBaseMessage, data interface{}) (LowAuxiliaryPriorityMessage, error) {
	rawData, err := json.Marshal(data)
	if err != nil {
		return LowAuxiliaryPriorityMessage{}, err
	}
	return LowAuxiliaryPriorityMessage{
		ACARSBaseMessage: base,
		Data:             rawData,
	}, nil
}
