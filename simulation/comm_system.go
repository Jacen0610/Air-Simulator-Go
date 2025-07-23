package simulation

import (
	"log"
	"sync"
	"time"
)

// CommunicationSystem 封装了主备双信道，为实体提供统一的通信接口。
type CommunicationSystem struct {
	PrimaryChannel *Channel
	BackupChannel  *Channel // 在单信道模式下，此字段为 nil

	// --- 新增: 动态时隙和其读写锁 ---
	currentTimeSlot time.Duration
	timeSlotMutex   sync.RWMutex
}

// NewCommunicationSystem 是 CommunicationSystem 的构造函数。
func NewCommunicationSystem(primary, backup *Channel) *CommunicationSystem {
	return &CommunicationSystem{
		PrimaryChannel:  primary,
		BackupChannel:   backup,
		currentTimeSlot: TimeSlot, // 初始化为默认值
	}
}

// --- 新增: 动态更新和获取时隙的方法 ---

// UpdateCurrentTimeSlot 允许 RL Agent 动态更新时隙。
func (cs *CommunicationSystem) UpdateCurrentTimeSlot(newTimeSlot time.Duration) {
	cs.timeSlotMutex.Lock()
	defer cs.timeSlotMutex.Unlock()
	cs.currentTimeSlot = newTimeSlot
}

// GetCurrentTimeSlot 安全地获取当前的时隙值。
func (cs *CommunicationSystem) GetCurrentTimeSlot() time.Duration {
	cs.timeSlotMutex.RLock()
	defer cs.timeSlotMutex.RUnlock()
	return cs.currentTimeSlot
}

// RegisterListener 将一个监听者注册到所有可用的信道。
func (cs *CommunicationSystem) RegisterListener(listener chan<- ACARSMessageInterface) {
	cs.PrimaryChannel.RegisterListener(listener)
	if cs.BackupChannel != nil {
		cs.BackupChannel.RegisterListener(listener)
	}
}

// SelectChannelForMessage 根据报文优先级和信道状态选择合适的信道。
func (cs *CommunicationSystem) SelectChannelForMessage(msg ACARSMessageInterface, senderID string) *Channel {
	if cs.BackupChannel == nil {
		return cs.PrimaryChannel
	}

	priority := msg.GetPriority()

	if priority == CriticalPriority || priority == HighPriority {
		if !cs.PrimaryChannel.IsBusy() {
			return cs.PrimaryChannel
		}
		log.Printf("⚠️  [%s] 主信道忙，高优先级报文 (ID: %s) 切换至备用信道。", senderID, msg.GetBaseMessage().MessageID)
		return cs.BackupChannel
	}

	return cs.PrimaryChannel
}