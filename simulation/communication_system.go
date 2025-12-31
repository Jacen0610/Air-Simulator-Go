package simulation

import (
	"Air-Simulator/config"
	"log"
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
