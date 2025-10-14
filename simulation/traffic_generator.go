package simulation

import (
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"
)

// TrafficMode 定义了背景流量的模式
type TrafficMode string

const (
	ModeStable TrafficMode = "STABLE" // 平稳期
	ModePeak   TrafficMode = "PEAK"   // 高峰期
	ModeLow    TrafficMode = "LOW"    // 低谷期
)

// [新增] 背景流量生成器使用的固定 ICAO 地址
const BackgroundTrafficICAO = "BG_TRAFFIC"

// TrafficGenerator 结构体定义了一个背景流量生成器
type TrafficGenerator struct {
	ID          string
	commsSystem *CommunicationSystem
	currentMode TrafficMode
	modeMutex   sync.RWMutex
	stopChan    chan struct{}
	wg          sync.WaitGroup

	// [新增] 内部消息队列，用于存储待发送的背景消息
	messageQueue []ACARSMessageInterface
	queueMutex   sync.Mutex
}

// NewTrafficGenerator 创建一个新的背景流量生成器实例
func NewTrafficGenerator(commsSystem *CommunicationSystem) *TrafficGenerator {
	return &TrafficGenerator{
		ID:           BackgroundTrafficICAO, // 使用固定的 ICAO 地址
		commsSystem:  commsSystem,
		currentMode:  ModeStable, // 初始模式为平稳期
		stopChan:     make(chan struct{}),
		messageQueue: make([]ACARSMessageInterface, 0, 10),
	}
}

// Start 启动流量生成器的 goroutine
func (g *TrafficGenerator) Start() {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		log.Printf("🚦 [流量生成器 %s] 已启动。", g.ID)

		// 模式切换的计时器
		modeTicker := time.NewTicker(4 * time.Minute) // 每2分钟切换一次模式
		defer modeTicker.Stop()

		// 消息生成计时器 (用于将消息放入内部队列)
		var msgGenTicker *time.Ticker
		g.setTickerForCurrentMode(&msgGenTicker)
		defer msgGenTicker.Stop()

		// 消息发送计时器 (用于驱动 CSMA 尝试发送)
		csmaTicker := time.NewTicker(g.commsSystem.PrimaryChannel.GetCurrentTimeSlot()) // 使用信道时隙作为CSMA的步进
		defer csmaTicker.Stop()

		for {
			select {
			case <-g.stopChan:
				log.Printf("🚦 [流量生成器 %s] 已停止。", g.ID)
				return

			case <-modeTicker.C:
				// 切换到下一个模式
				g.switchToNextMode()
				// 根据新模式重置消息生成计时器
				g.setTickerForCurrentMode(&msgGenTicker)

			case <-msgGenTicker.C:
				// 生成一条新消息并放入内部队列
				newMessage := g.generateBackgroundMessage()
				if newMessage != nil {
					g.queueMutex.Lock()
					g.messageQueue = append(g.messageQueue, newMessage)
					g.queueMutex.Unlock()
					//log.Printf("📥 [流量生成器 %s] 新背景消息 (ID: %s) 已入队。", g.ID, newMessage.GetBaseMessage().MessageID)
				}

			case <-csmaTicker.C:
				// 驱动 P-坚持 CSMA 逻辑
				g.attemptSendCSMA()
			}
		}
	}()
}

// Stop 停止流量生成器
func (g *TrafficGenerator) Stop() {
	close(g.stopChan)
	g.wg.Wait()
}

// Reset 重置流量生成器状态，用于新的 episode
func (g *TrafficGenerator) Reset() {
	// 停止当前的 goroutine
	g.Stop()
	// 创建新的 stopChan 并重新启动
	g.stopChan = make(chan struct{})
	g.modeMutex.Lock()
	g.currentMode = ModeStable // 总是从平稳期开始
	g.modeMutex.Unlock()

	g.queueMutex.Lock()
	g.messageQueue = make([]ACARSMessageInterface, 0, 10) // 清空消息队列
	g.queueMutex.Unlock()

	g.Start()
}

// setTickerForCurrentMode 根据当前流量模式设置消息生成速率 (已修改，引入随机抖动)
func (g *TrafficGenerator) setTickerForCurrentMode(ticker **time.Ticker) {
	g.modeMutex.RLock()
	defer g.modeMutex.RUnlock()

	if *ticker != nil {
		(*ticker).Stop()
	}

	var baseInterval time.Duration
	var jitterRange time.Duration // 最大的额外随机延迟

	switch g.currentMode {
	case ModePeak:
		baseInterval = 800 * time.Millisecond
		jitterRange = 200 * time.Millisecond // 消息间隔将在 200ms 到 300ms 之间随机
	case ModeStable:
		baseInterval = 2000 * time.Millisecond
		jitterRange = 500 * time.Millisecond // 消息间隔将在 800ms 到 1200ms 之间随机
	case ModeLow:
		baseInterval = 5 * time.Second
		jitterRange = 1 * time.Second // 消息间隔将在 2s 到 3s 之间随机
	}

	// 计算随机抖动，并添加到基础间隔上
	randomJitter := time.Duration(rand.Int63n(int64(jitterRange)))
	interval := baseInterval + randomJitter

	*ticker = time.NewTicker(interval)
	log.Printf("🚦 [流量生成器 %s] 模式已切换到 %s，消息生成间隔: %s (基准: %s, 抖动: %s)", g.ID, g.currentMode, interval, baseInterval, randomJitter)
}

// switchToNextMode 按照预设顺序切换流量模式
func (g *TrafficGenerator) switchToNextMode() {
	g.modeMutex.Lock()
	defer g.modeMutex.Unlock()

	switch g.currentMode {
	case ModeStable:
		g.currentMode = ModePeak
	case ModePeak:
		g.currentMode = ModeLow
	case ModeLow:
		g.currentMode = ModeStable
	}
}

// generateBackgroundMessage 生成一条背景消息并返回 (不再尝试发送)
func (g *TrafficGenerator) generateBackgroundMessage() ACARSMessageInterface {
	baseMsg := ACARSBaseMessage{
		AircraftICAOAddress: BackgroundTrafficICAO, // 固定背景流量的 ICAO
		MessageID:           fmt.Sprintf("BG_MSG_%d", time.Now().UnixNano()),
		Timestamp:           time.Now(),
	}

	// 所有背景消息都使用 MediumPriority
	msg, err := NewMediumPriorityMessage(baseMsg, "Background traffic message")
	if err != nil {
		log.Printf("错误: [流量生成器 %s] 创建背景消息失败: %v", g.ID, err)
		return nil
	}
	return msg
}

// attemptSendCSMA 尝试使用 P-坚持 CSMA 算法发送队列中的第一条消息
func (g *TrafficGenerator) attemptSendCSMA() {
	g.queueMutex.Lock()
	defer g.queueMutex.Unlock()

	if len(g.messageQueue) == 0 {
		return // 队列为空，无需发送
	}

	msg := g.messageQueue[0] // 获取队列头部的消息

	// P-persistence 参数
	p := 0.05 // 可以根据需要调整，这里设置为0.5

	// 1. 载波侦听
	if g.commsSystem.PrimaryChannel.IsBusy() {
		// log.Printf("🚦 [流量生成器 %s] 信道忙，延迟发送背景消息 (ID: %s)。", g.ID, msg.GetBaseMessage().MessageID)
		return // 信道忙，等待下一个时隙再尝试
	}

	// 2. 信道空闲，以概率 p 尝试发送
	if rand.Float64() < p {
		// 模拟发送前的随机延迟，作为CSMA的一部分
		time.Sleep(time.Duration(10+rand.Intn(41)) * time.Microsecond)

		transmitted := g.commsSystem.PrimaryChannel.AttemptTransmit(msg, g.ID, 200*time.Millisecond) // 假设背景消息较短
		if transmitted {
			//log.Printf("✅ [流量生成器 %s] 成功发送背景消息 (ID: %s)。", g.ID, msg.GetBaseMessage().MessageID)
		}
		g.messageQueue = g.messageQueue[1:]
	} else {
		// 以 1-p 的概率决定延迟一个时隙
		// log.Printf("🤔 [流量生成器 %s] 信道空闲，但决定延迟发送背景消息 (ID: %s)。", g.ID, msg.GetBaseMessage().MessageID)
	}
}
