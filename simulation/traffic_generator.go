package simulation

import (
	"Air-Simulator/config"
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
	ModeBurst  TrafficMode = "BURST"  // [新] 突发期
)

// 背景流量生成器使用的固定 ICAO 地址
const BackgroundTrafficICAO = "BG_TRAFFIC"

// TrafficGenerator 结构体定义了一个背景流量生成器
type TrafficGenerator struct {
	ID           string
	commsSystem  *CommunicationSystem
	currentMode  TrafficMode
	modeMutex    sync.RWMutex
	stopChan     chan struct{}
	wg           sync.WaitGroup
	messageQueue []ACARSMessageInterface
	queueMutex   sync.Mutex
}

// NewTrafficGenerator 创建一个新的背景流量生成器实例
func NewTrafficGenerator(commsSystem *CommunicationSystem) *TrafficGenerator {
	return &TrafficGenerator{
		ID:           BackgroundTrafficICAO,
		commsSystem:  commsSystem,
		currentMode:  ModeStable,
		stopChan:     make(chan struct{}),
		messageQueue: make([]ACARSMessageInterface, 0, 20), // 增加队列容量以应对突发
	}
}

// Start 启动流量生成器的 goroutine
func (g *TrafficGenerator) Start() {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		log.Printf("🚦 [流量生成器 %s] 已启动。", g.ID)

		// 模式切换的计时器
		modeTicker := time.NewTicker(90 * time.Second) // 缩短模式切换周期，让变化更频繁
		defer modeTicker.Stop()

		var msgGenTicker *time.Ticker
		g.setTickerForCurrentMode(&msgGenTicker)
		defer msgGenTicker.Stop()

		csmaTicker := time.NewTicker(g.commsSystem.PrimaryChannel.GetCurrentTimeSlot())
		defer csmaTicker.Stop()

		for {
			select {
			case <-g.stopChan:
				log.Printf("🚦 [流量生成器 %s] 已停止。", g.ID)
				return
			case <-modeTicker.C:
				g.switchToNextMode()
				g.setTickerForCurrentMode(&msgGenTicker)
			case <-msgGenTicker.C:
				newMessage := g.generateBackgroundMessage()
				if newMessage != nil {
					g.queueMutex.Lock()
					g.messageQueue = append(g.messageQueue, newMessage)
					g.queueMutex.Unlock()
				}
			case <-csmaTicker.C:
				g.attemptSendCSMA()
			}
		}
	}()
}

// Stop 停止流量生成器
func (g *TrafficGenerator) Stop() {
	// ... (代码无变化)
	close(g.stopChan)
	g.wg.Wait()
}

// Reset 重置流量生成器状态
func (g *TrafficGenerator) Reset() {
	// ... (代码无变化)
	g.Stop()
	g.stopChan = make(chan struct{})
	g.modeMutex.Lock()
	g.currentMode = ModeStable
	g.modeMutex.Unlock()
	g.queueMutex.Lock()
	g.messageQueue = make([]ACARSMessageInterface, 0, 20)
	g.queueMutex.Unlock()
	g.Start()
}

// [大幅修改] setTickerForCurrentMode 根据当前流量模式设置消息生成速率
func (g *TrafficGenerator) setTickerForCurrentMode(ticker **time.Ticker) {
	g.modeMutex.RLock()
	defer g.modeMutex.RUnlock()

	if *ticker != nil {
		(*ticker).Stop()
	}

	var baseInterval time.Duration
	var jitterRange time.Duration

	switch g.currentMode {
	case ModeBurst: // [新] 突发模式，流量极度密集
		baseInterval = 40 * time.Millisecond * config.BaseIntervalMultiple
		jitterRange = 30 * time.Millisecond // 间隔在 40ms ~ 70ms
	case ModePeak: // 高峰期，非常密集
		baseInterval = 150 * time.Millisecond * config.BaseIntervalMultiple
		jitterRange = 100 * time.Millisecond // 间隔在 150ms ~ 250ms
	case ModeStable: // 平稳期，中等密度
		baseInterval = 600 * time.Millisecond * config.BaseIntervalMultiple
		jitterRange = 400 * time.Millisecond // 间隔在 600ms ~ 1000ms
	case ModeLow: // 低谷期，非常稀疏
		baseInterval = 3 * time.Second
		jitterRange = 2 * time.Second // 间隔在 3s ~ 5s
	}

	randomJitter := time.Duration(rand.Int63n(int64(jitterRange)))
	interval := baseInterval + randomJitter

	*ticker = time.NewTicker(interval)
	log.Printf("🚦 [流量生成器 %s] 模式已切换到 %s，消息生成间隔: %s", g.ID, g.currentMode, interval)
}

// [修改] switchToNextMode 按照新的顺序切换流量模式
func (g *TrafficGenerator) switchToNextMode() {
	g.modeMutex.Lock()
	defer g.modeMutex.Unlock()

	switch g.currentMode {
	case ModeStable:
		g.currentMode = ModePeak
	case ModePeak:
		g.currentMode = ModeBurst // 高峰后进入突发
	case ModeBurst:
		g.currentMode = ModeLow // 突发后进入低谷，形成强烈对比
	case ModeLow:
		g.currentMode = ModeStable
	}
}

// generateBackgroundMessage 生成一条背景消息 (代码无变化)
func (g *TrafficGenerator) generateBackgroundMessage() ACARSMessageInterface {
	baseMsg := ACARSBaseMessage{
		AircraftICAOAddress: BackgroundTrafficICAO,
		MessageID:           fmt.Sprintf("BG_MSG_%d", time.Now().UnixNano()),
		Timestamp:           time.Now(),
	}
	msg, err := NewMediumPriorityMessage(baseMsg, "Background traffic message")
	if err != nil {
		log.Printf("错误: [流量生成器 %s] 创建背景消息失败: %v", g.ID, err)
		return nil
	}
	return msg
}

// attemptSendCSMA 使用 P-坚持 CSMA 算法发送消息 (代码无变化)
func (g *TrafficGenerator) attemptSendCSMA() {
	g.queueMutex.Lock()
	defer g.queueMutex.Unlock()

	if len(g.messageQueue) == 0 {
		return
	}

	msg := g.messageQueue[0]
	p := 0.5

	if g.commsSystem.PrimaryChannel.IsBusy() {
		return
	}

	if rand.Float64() < p {
		time.Sleep(time.Duration(10+rand.Intn(41)) * time.Microsecond)
		transmitted := g.commsSystem.PrimaryChannel.AttemptTransmit(msg, g.ID, 200*time.Millisecond)
		if transmitted {
			g.messageQueue = g.messageQueue[1:]
		}
	}
}
