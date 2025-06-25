package main

import (
	"encoding/json"
	"testing" // 导入 testing 包
	"time"
)

// TestCreateAndParseCriticalMessage 测试紧急/高优先级消息的创建和解析。
func TestCreateAndParseCriticalMessage(t *testing.T) {
	// 1. 创建基础消息头部
	baseMsg := ACARSBaseMessage{
		AircraftICAOAddress: "A98765",
		FlightID:            "CES123",
		Timestamp:           time.Now(),
		MessageID:           "TEST-FAULT-001",
		Type:                MsgTypeAircraftFault,
	}

	// 2. 创建具体的故障数据
	faultData := AircraftFaultData{
		FaultCode:   "NAV-GPS-FAIL",
		Description: "GPS Unit 1 Failure",
		Severity:    "MAJOR",
		Timestamp:   time.Now().Add(-10 * time.Minute),
		System:      "NAVIGATION",
	}

	// 3. 创建 CriticalHighPriorityMessage
	criticalMsg, err := NewCriticalHighPriorityMessage(baseMsg, faultData)
	if err != nil {
		t.Fatalf("创建紧急消息失败: %v", err) // t.Fatalf 在出错时停止测试
	}

	t.Log(criticalMsg.GetPriority())

	// 4. 将消息序列化为 JSON (模拟发送)
	jsonBytes, err := json.Marshal(criticalMsg)
	if err != nil {
		t.Fatalf("将紧急消息序列化为 JSON 失败: %v", err)
	}

	// 5. 反序列化 JSON (模拟接收)
	var receivedCriticalMsg CriticalHighPriorityMessage
	err = json.Unmarshal(jsonBytes, &receivedCriticalMsg)
	if err != nil {
		t.Fatalf("从 JSON 反序列化紧急消息失败: %v", err)
	}

	// 6. 验证基础消息字段
	if receivedCriticalMsg.AircraftICAOAddress != baseMsg.AircraftICAOAddress {
		t.Errorf("期望的 ICAO 地址是 %s, 得到 %s", baseMsg.AircraftICAOAddress, receivedCriticalMsg.AircraftICAOAddress)
	}
	if receivedCriticalMsg.Type != MsgTypeAircraftFault {
		t.Errorf("期望的消息类型是 %s, 得到 %s", MsgTypeAircraftFault, receivedCriticalMsg.Type)
	}

	// 7. 反序列化并验证具体的 Data 字段
	var receivedFaultData AircraftFaultData
	err = json.Unmarshal(receivedCriticalMsg.Data, &receivedFaultData)
	if err != nil {
		t.Fatalf("从 RawMessage 反序列化故障数据失败: %v", err)
	}

	if receivedFaultData.FaultCode != faultData.FaultCode {
		t.Errorf("期望的故障代码是 %s, 得到 %s", faultData.FaultCode, receivedFaultData.FaultCode)
	}
	if receivedFaultData.Severity != faultData.Severity {
		t.Errorf("期望的严重性是 %s, 得到 %s", faultData.Severity, receivedFaultData.Severity)
	}

	t.Log("TestCreateAndParseCriticalMessage 测试通过。") // 记录测试成功
}
