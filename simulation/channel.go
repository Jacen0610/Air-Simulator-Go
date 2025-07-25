// C:/workspace/go/Air-Simulator-Go/channel.go
package simulation

import (
	"Air-Simulator/config"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// Channel 模拟一个共享的物理通信信道。
type Channel struct {
	ID            string
	mutex         sync.Mutex
	isBusy        bool
	messageQueue  chan ACARSMessageInterface
	listeners     []chan<- ACARSMessageInterface
	listenerMutex sync.Mutex

	// --- 统计字段 ---
	totalMessagesTransmitted atomic.Uint64
	totalBusyTime            time.Duration
	lastBusyTimestamp        time.Time

	// --- 可动态更新的 p-value 策略 ---
	pValues      map[config.Priority]float64
	pValuesMutex sync.RWMutex

	// --- 时隙 (TimeSlot) ---
	currentTimeSlot time.Duration // 新增: 时隙现在是信道的属性
	timeSlotMutex   sync.RWMutex
}

// NewChannel 是 Channel 的构造函数。
func NewChannel(id string, initialPMap map[config.Priority]float64, initialTimeSlot time.Duration) *Channel {
	return &Channel{
		ID:              id,
		messageQueue:    make(chan ACARSMessageInterface, 100),
		listeners:       make([]chan<- ACARSMessageInterface, 0),
		pValues:         initialPMap,
		currentTimeSlot: initialTimeSlot,
	}
}

func (c *Channel) UpdatePValues(newPMap map[config.Priority]float64) {
	c.pValuesMutex.Lock()
	defer c.pValuesMutex.Unlock()
	c.pValues = newPMap
	log.Printf("🔄 信道 [%s] 的 p-map 已更新。", c.ID)
}

// GetPForMessage 为给定的优先级获取当前的 p-value。
func (c *Channel) GetPForMessage(priority config.Priority) float64 {
	c.pValuesMutex.RLock()
	defer c.pValuesMutex.RUnlock()
	if p, ok := c.pValues[config.Priority(priority)]; ok {
		return p
	}
	return 0.1 // 返回一个安全的默认值
}

// UpdateCurrentTimeSlot 允许动态更新时隙。
func (c *Channel) UpdateCurrentTimeSlot(newTimeSlot time.Duration) {
	c.timeSlotMutex.Lock()
	defer c.timeSlotMutex.Unlock()
	c.currentTimeSlot = newTimeSlot
	log.Printf("🔄 信道 [%s] 的时隙已更新为 %v。", c.ID, newTimeSlot)
}

// GetCurrentTimeSlot 安全地获取当前的时隙值。
func (c *Channel) GetCurrentTimeSlot() time.Duration {
	c.timeSlotMutex.RLock()
	defer c.timeSlotMutex.RUnlock()
	return c.currentTimeSlot
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
	go func() {
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
	}()
}

// GetTotalBusyTime 安全地返回总占用时间
func (c *Channel) GetTotalBusyTime() time.Duration {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.totalBusyTime
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
