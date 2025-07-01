// C:/workspace/go/Air-Simulator-Go/channel.go
package main

import (
	"log"
	"sync"
	"time"
)

// Channel 模拟一个共享的物理通信信道。
// 它只负责管理信道的忙/闲状态和广播消息，不包含任何退避逻辑。
type Channel struct {
	mutex         sync.Mutex
	isBusy        bool
	messageQueue  chan ACARSMessageInterface
	listeners     []chan<- ACARSMessageInterface
	listenerMutex sync.Mutex
}

// NewChannel 是 Channel 的构造函数。
func NewChannel() *Channel {
	return &Channel{
		messageQueue: make(chan ACARSMessageInterface, 100),
		listeners:    make([]chan<- ACARSMessageInterface, 0),
	}
}

// IsBusy 检查信道当前是否被占用。
func (c *Channel) IsBusy() bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.isBusy
}

// AttemptTransmit 尝试在信道上传输一个报文。
// 如果信道空闲，它会占用信道，模拟传输延迟，然后广播报文并释放信道。
// 它会立即返回一个布尔值，指示传输是否成功开始。
// 占用、传输、释放的过程在一个新的 goroutine 中异步完成，以避免阻塞调用者。
func (c *Channel) AttemptTransmit(msg ACARSMessageInterface, senderID string, transmissionTime time.Duration) bool {
	c.mutex.Lock()
	if c.isBusy {
		// 信道忙，立即返回失败
		c.mutex.Unlock()
		return false
	}
	// 信道空闲，占用它！
	c.isBusy = true
	c.mutex.Unlock()

	log.Printf("➡️  [%s] 成功获得信道，开始传输报文 (ID: %s)", senderID, msg.GetBaseMessage().MessageID)

	go func() {
		// 1. 模拟传输延迟
		time.Sleep(transmissionTime)

		// 2. 报文“到达”信道，放入调度队列
		c.messageQueue <- msg
		log.Printf("✅ [%s] 报文 (ID: %s) 已成功发送至信道。", senderID, msg.GetBaseMessage().MessageID)

		// 3. 释放信道
		c.mutex.Lock()
		c.isBusy = false
		c.mutex.Unlock()
		log.Printf("⬅️  [%s] 传输完成，释放信道。", senderID)
	}()

	return true // 传输已成功开始
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
