package simulation

import (
	"Air-Simulator/config"
	"log"
	"math/rand"
	"sync"
)

// CommunicationSystem 封装了主备双信道，为实体提供统一的通信接口。
type CommunicationSystem struct {
	PrimaryChannel *Channel
	BackupChannel  *Channel // 在单信道模式下，此字段为 nil

	switchoverProbabilities      map[config.Priority]float64
	switchoverProbabilitiesMutex sync.RWMutex
}

// NewCommunicationSystem 是 CommunicationSystem 的构造函数。
func NewCommunicationSystem(primary, backup *Channel, initialProbs map[config.Priority]float64) *CommunicationSystem {
	// 最佳实践：创建一个副本，以避免外部对原始map的修改影响到系统内部状态
	probs := make(map[config.Priority]float64)
	if initialProbs != nil {
		for k, v := range initialProbs {
			probs[k] = v
		}
	}

	return &CommunicationSystem{
		PrimaryChannel:          primary,
		BackupChannel:           backup,
		switchoverProbabilities: probs,
	}
}

func (cs *CommunicationSystem) UpdateSwitchoverProbabilities(newProbs map[config.Priority]float64) {
	cs.switchoverProbabilitiesMutex.Lock()
	defer cs.switchoverProbabilitiesMutex.Unlock()

	// 同样，创建一个新的map副本以保证数据隔离
	cs.switchoverProbabilities = make(map[config.Priority]float64)
	for k, v := range newProbs {
		cs.switchoverProbabilities[k] = v
	}
	log.Printf("🔄 通信系统的备用信道切换概率已更新。")
}

func (cs *CommunicationSystem) StartDispatching() {
	if cs.PrimaryChannel != nil {
		cs.PrimaryChannel.StartDispatching()
	}
	if cs.BackupChannel != nil {
		cs.BackupChannel.StartDispatching()
	}
}

// RegisterListener 将一个监听者注册到所有可用的信道。
func (cs *CommunicationSystem) RegisterListener(listener chan<- ACARSMessageInterface) {
	cs.PrimaryChannel.RegisterListener(listener)
	if cs.BackupChannel != nil {
		cs.BackupChannel.RegisterListener(listener)
	}
}

func (cs *CommunicationSystem) SelectChannelForMessage(msg ACARSMessageInterface, senderID string) *Channel {
	// 规则 1: 如果没有备用信道，总是返回主信道。
	if cs.BackupChannel == nil {
		return cs.PrimaryChannel
	}

	// 规则 2: 如果主备信道都存在，则随机选择一个。
	// rand.Intn(2) 会等概率地返回 0 或 1。
	if rand.Intn(2) == 0 {
		log.Printf("📡 [%s] 随机选择主信道 [%s] 发送报文 (ID: %s)。", senderID, cs.PrimaryChannel.ID, msg.GetBaseMessage().MessageID)
		return cs.PrimaryChannel
	}

	log.Printf("📡 [%s] 随机选择备用信道 [%s] 发送报文 (ID: %s)。", senderID, cs.BackupChannel.ID, msg.GetBaseMessage().MessageID)
	return cs.BackupChannel
}
