package simulation

import (
	"Air-Simulator/config"
	"log"
	"math/rand/v2"
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

// SelectChannelForMessage 根据报文优先级和信道状态选择合适的信道。
func (cs *CommunicationSystem) SelectChannelForMessage(msg ACARSMessageInterface, senderID string) *Channel {
	// 规则 1: 如果没有备用信道，或者主信道空闲，总是使用主信道。
	if cs.BackupChannel == nil || !cs.PrimaryChannel.IsBusy() {
		return cs.PrimaryChannel
	}

	// 规则 2: 主信道忙碌，从系统属性中安全地读取切换概率
	cs.switchoverProbabilitiesMutex.RLock()
	priority := msg.GetPriority()
	// 从map中获取当前优先级的切换概率，如果不存在则默认为0
	switchoverP := cs.switchoverProbabilities[priority]
	cs.switchoverProbabilitiesMutex.RUnlock()

	// 规则 3: 执行概率判断。如果随机数小于设定的概率，则切换。
	if rand.Float64() < switchoverP {
		// 切换成功
		log.Printf("⚠️  [%s] 主信道忙，报文 (ID: %s, Prio: %s) 概率切换 (p=%.2f) 至备用信道 [%s]。",
			senderID, msg.GetBaseMessage().MessageID, priority, switchoverP, cs.BackupChannel.ID)
		return cs.BackupChannel
	}

	// 规则 4: 概率判断未通过，或概率为0，继续等待主信道。
	if switchoverP > 0 {
		log.Printf("⏳ [%s] 主信道忙，报文 (ID: %s, Prio: %s) 概率决定 (p=%.2f) 等待主信道 [%s]。",
			senderID, msg.GetBaseMessage().MessageID, priority, switchoverP, cs.PrimaryChannel.ID)
	}

	return cs.PrimaryChannel
}
