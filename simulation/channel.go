// C:/workspace/go/Air-Simulator-Go/channel.go
package simulation

import (
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// Channel 模拟一个共享的物理通信信道。
type Channel struct {
	mutex         sync.Mutex
	isBusy        bool
	messageQueue  chan ACARSMessageInterface
	listeners     []chan<- ACARSMessageInterface
	listenerMutex sync.Mutex

	// --- 统计字段 ---
	totalMessagesTransmitted atomic.Uint64
	totalBusyTime            time.Duration
	lastBusyTimestamp        time.Time

	// --- 新增: 可动态更新的 p-value 策略 ---
	pValues      PriorityPMap
	pValuesMutex sync.RWMutex
}

// NewChannel 是 Channel 的构造函数。
func NewChannel(initialPMap PriorityPMap) *Channel {
	return &Channel{
		messageQueue: make(chan ACARSMessageInterface, 100),
		listeners:    make([]chan<- ACARSMessageInterface, 0),
		pValues:      initialPMap, // 设置初始策略
	}
}

// --- 新增: 动态更新和获取策略的方法 ---

// UpdatePValues 允许 RL Agent 动态更新信道的 p-value 策略。
func (c *Channel) UpdatePValues(newPMap PriorityPMap) {
	c.pValuesMutex.Lock()
	defer c.pValuesMutex.Unlock()
	c.pValues = newPMap
}

// GetPForMessage 为给定的优先级获取当前的 p-value。
func (c *Channel) GetPForMessage(priority Priority) float64 {
	c.pValuesMutex.RLock()
	defer c.pValuesMutex.RUnlock()
	if p, ok := c.pValues[priority]; ok {
		return p
	}
	return 0.1 // 返回一个安全的默认值
}

// GetTotalBusyTime 安全地返回总占用时间
func (c *Channel) GetTotalBusyTime() time.Duration {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.totalBusyTime
}

// IsBusy 检查信道当前是否被占用。
func (c *Channel) IsBusy() bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.isBusy
}

// AttemptTransmit 尝试在信道上传输一个报文。
func (c *Channel) AttemptTransmit(msg ACARSMessageInterface, senderID string, transmissionTime time.Duration) bool {
	c.mutex.Lock()
	if c.isBusy {
		c.mutex.Unlock()
		return false
	}
	c.isBusy = true
	c.lastBusyTimestamp = time.Now()
	c.mutex.Unlock()

	log.Printf("➡️  [%s] 成功获得信道，开始传输报文 (ID: %s)", senderID, msg.GetBaseMessage().MessageID)

	go func() {
		time.Sleep(transmissionTime)
		c.messageQueue <- msg
		c.totalMessagesTransmitted.Add(1)
		log.Printf("✅ [%s] 报文 (ID: %s) 已成功发送至信道。", senderID, msg.GetBaseMessage().MessageID)

		c.mutex.Lock()
		c.isBusy = false
		busyDuration := time.Since(c.lastBusyTimestamp)
		c.totalBusyTime += busyDuration
		c.mutex.Unlock()
		log.Printf("⬅️  [%s] 传输完成，释放信道。", senderID)
	}()

	return true
}

// RegisterListener 和 StartDispatching 保持不变
func (c *Channel) RegisterListener(listener chan<- ACARSMessageInterface) {
	c.listenerMutex.Lock()
	defer c.listenerMutex.Unlock()
	c.listeners = append(c.listeners, listener)
}

func (c *Channel) StartDispatching() {
	log.Println("📡 信道调度服务已启动...")
	for msg := range c.messageQueue {
		c.listenerMutex.Lock()
		for _, listener := range c.listeners {
			select {
			case listener <- msg:
			default:
				log.Printf("警告: 监听者队列已满，消息 %s 被丢弃。", msg.GetBaseMessage().MessageID)
			}
		}
		c.listenerMutex.Unlock()
	}
}

// GetStats 返回一个包含信道统计信息的可读字符串。
func (c *Channel) GetStats(totalDuration time.Duration) string {
	busyTime := c.GetTotalBusyTime()
	var utilizationRate float64
	if totalDuration > 0 {
		utilizationRate = (float64(busyTime) / float64(totalDuration)) * 100
	}

	stats := fmt.Sprintf("--- 信道统计 ---\n")
	stats += fmt.Sprintf("  - 总传输报文数: %d\n", c.totalMessagesTransmitted.Load())
	stats += fmt.Sprintf("  - 总占用时间: %v\n", busyTime.Round(time.Millisecond))
	stats += fmt.Sprintf("  - 信道占用率: %.2f%%\n", utilizationRate)
	stats += "------------------\n"
	return stats
}

func (c *Channel) ResetStats() {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.totalBusyTime = 0

	c.totalMessagesTransmitted.Store(0)
}

// ChannelRawStats Excel自动统计需要以下两个函数
type ChannelRawStats struct {
	TotalMessagesTransmitted uint64
	TotalBusyTime            time.Duration
}

func (c *Channel) GetRawStats() ChannelRawStats {
	return ChannelRawStats{
		TotalMessagesTransmitted: c.totalMessagesTransmitted.Load(),
		TotalBusyTime:            c.GetTotalBusyTime(),
	}
}
