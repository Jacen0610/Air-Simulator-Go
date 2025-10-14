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
}

// NewTrafficGenerator 创建一个新的背景流量生成器实例
func NewTrafficGenerator(commsSystem *CommunicationSystem) *TrafficGenerator {
	return &TrafficGenerator{
		ID:          BackgroundTrafficICAO, // 使用固定的 ICAO 地址
		commsSystem: commsSystem,
		currentMode: ModeStable, // 初始模式为平稳期
		stopChan:    make(chan struct{}),
	}
}

// Start 启动流量生成器的 goroutine
func (g *TrafficGenerator) Start() {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		log.Printf("🚦 [流量生成器 %s] 已启动。", g.ID)

		// 模式切换的计时器
		modeTicker := time.NewTicker(2 * time.Minute) // 每2分钟切换一次模式
		defer modeTicker.Stop()

		// 消息生成的计时器
		var msgTicker *time.Ticker
		g.setTickerForCurrentMode(&msgTicker)
		defer msgTicker.Stop()

		for {
			select {
			case <-g.stopChan:
				log.Printf("🚦 [流量生成器 %s] 已停止。", g.ID)
				return

			case <-modeTicker.C:
				// 切换到下一个模式
				g.switchToNextMode()
				// 根据新模式重置消息生成计时器
				g.setTickerForCurrentMode(&msgTicker)

			case <-msgTicker.C:
				// 生成并尝试发送一条背景消息
				g.generateAndSendMessage()
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
		baseInterval = 600 * time.Millisecond
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

// generateAndSendMessage 生成一条随机消息并尝试直接在信道上发送 (已修改，固定 ICAO 和优先级)
func (g *TrafficGenerator) generateAndSendMessage() {

	var msg ACARSMessageInterface
	var err error

	// [修改] 固定 ICAO 地址和优先级
	baseMsg := ACARSBaseMessage{
		AircraftICAOAddress: BackgroundTrafficICAO, // 固定背景流量的 ICAO
		MessageID:           fmt.Sprintf("BG_MSG_%d", time.Now().UnixNano()),
		Timestamp:           time.Now(),
	}

	// [修改] 所有背景消息都使用 MediumPriority
	msg, err = NewMediumPriorityMessage(baseMsg, "Background traffic message")

	if err != nil {
		log.Printf("错误: [流量生成器 %s] 创建背景消息失败: %v", g.ID, err)
		return
	}

	// 直接尝试在主信道上传输，模拟其他实体抢占信道的行为
	// 这会与飞机和地面站产生碰撞
	transmitted := g.commsSystem.PrimaryChannel.AttemptTransmit(msg, g.ID, config.TransmissionTime) // 假设背景消息较短
	if !transmitted {
		log.Printf("🚦 [流量生成器 %s] 尝试发送消息失败（信道忙或碰撞）。", g.ID)
	}
}
