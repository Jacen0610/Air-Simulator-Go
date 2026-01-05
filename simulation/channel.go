// C:/workspace/go/Air-Simulator-Go/channel.go
package simulation

import (
	"Air-Simulator/config"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// jamTime 定义了在发生碰撞后，信道因信号混乱而保持不可用的时间。
	jamTime = 440 * time.Millisecond
)

// Channel 模拟一个共享的物理通信信道。
type Channel struct {
	ID             string
	stateMutex     sync.Mutex
	isBusy         bool
	transmissionID atomic.Uint64 // **[修改]** 用于识别和作废传输的唯一ID

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
	currentTimeSlot time.Duration
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

// [新增] ForceTransmit 强制发送高优先级消息，绕过CSMA竞争
// 主要用于ACK等关键控制信令，以防止饿死。
func (c *Channel) ForceTransmit(msg ACARSMessageInterface, senderID string) {
	select {
	case c.messageQueue <- msg:
		log.Printf("✅ [%s] 通过优先信道强制发送报文 (ID: %s)。", senderID, msg.GetBaseMessage().MessageID)
		c.totalMessagesTransmitted.Add(1) // 同样计入总数
	default:
		log.Printf("⚠️ [%s] 强制发送失败，信道消息队列已满！", senderID)
	}
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
	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()
	return c.isBusy
}

// **[核心修改]** 改为同步阻塞模型，函数在传输完成或被破坏后才返回，并返回最终结果。
func (c *Channel) AttemptTransmit(msg ACARSMessageInterface, senderID string, transmissionTime time.Duration) bool {
	// 为本次传输尝试生成一个唯一的ID。
	myID := c.transmissionID.Add(1)

	c.stateMutex.Lock()

	// 如果信道已被占用，则发生碰撞。
	if c.isBusy {
		// 通过再次增加ID，使正在进行的传输失效。
		c.transmissionID.Add(1)
		log.Printf("💥 [%s] 在繁忙的信道 %s 上尝试传输，引发碰撞！正在进行的传输被破坏。", senderID, c.ID)

		// 启动一个goroutine来处理“拥塞”时间，之后再释放信道。
		go c.jamChannel()

		c.stateMutex.Unlock()
		return false // 本次尝试失败
	}

	// 信道空闲，我们占用它。
	c.isBusy = true
	c.lastBusyTimestamp = time.Now()
	c.stateMutex.Unlock()

	// log.Printf("➡️  [%s] 发现信道 %s 空闲，开始传输 (报文ID: %s, 传输ID: %d)，将阻塞 %v。", senderID, c.ID, msg.GetBaseMessage().MessageID, myID, transmissionTime)

	// **[修改]** 同步阻塞，模拟传输过程。
	time.Sleep(transmissionTime)

	// 传输结束后，重新获取锁并检查我们的传输ID是否仍然有效。
	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()

	// 检查我们的传输ID是否仍然有效。
	if c.transmissionID.Load() == myID {
		// 成功：传输未被中断。
		c.messageQueue <- msg
		c.totalMessagesTransmitted.Add(1)
		c.isBusy = false // 释放信道
		busyDuration := time.Since(c.lastBusyTimestamp)
		c.totalBusyTime += busyDuration
		if senderID != "BG_TRAFFIC" {
			log.Printf("✅ [%s] 在 %s 上的传输 (传输ID: %d) 成功完成。信道已释放。", senderID, c.ID, myID)
		}
		return true // **[修改]** 返回 true 表示最终成功
	} else {
		// 失败：我们的传输被后续的碰撞所破坏。
		// 碰撞的制造者已经负责处理拥塞和信道释放，我们只需记录失败并返回 false。
		log.Printf("❌ [%s] 在 %s 上的传输 (传输ID: %d) 被碰撞破坏。当前有效ID: %d。", senderID, c.ID, myID, c.transmissionID.Load())
		// 注意：我们不在这里释放信道 (c.isBusy = false)，因为碰撞的制造者已经通过 jamChannel 安排了信道的释放。
		return false // **[修改]** 返回 false 表示最终失败
	}
}

// **[新增]** jamChannel 用于在碰撞后将信道标记为拥塞，并在一段时间后清除。
func (c *Channel) jamChannel() {
	time.Sleep(jamTime)
	c.stateMutex.Lock()
	c.isBusy = false
	log.Printf("💥 信道 %s 的拥塞状态已清除。", c.ID)
	c.stateMutex.Unlock()
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
	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()
	return c.totalBusyTime
}

func (c *Channel) ResetStats() {
	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()
	c.totalBusyTime = 0
	c.transmissionID.Store(0)
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
