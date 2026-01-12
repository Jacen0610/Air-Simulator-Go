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

	// [新增] 独立的随机数生成器
	trafficRng *rand.Rand // 用于控制流量生成的宏观节奏 (Offset: 1)
	csmaRng    *rand.Rand // 用于控制CSMA发送的微观随机性 (Offset: 2)

	// [新增] 标志位：是否是第一次重置
	isFirstReset bool

	// [新增] 记录当前的生成间隔
	currentInterval time.Duration
}

// NewTrafficGenerator 创建一个新的背景流量生成器实例
func NewTrafficGenerator(commsSystem *CommunicationSystem) *TrafficGenerator {
	return &TrafficGenerator{
		ID:           BackgroundTrafficICAO,
		commsSystem:  commsSystem,
		currentMode:  ModeStable,
		stopChan:     make(chan struct{}),
		messageQueue: make([]ACARSMessageInterface, 0, 20),
		// [新增] 初始化独立的RNG
		trafficRng: config.NewRand(1),
		csmaRng:    config.NewRand(2),
		// [新增] 初始化标志位
		isFirstReset: true,
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

	// [修改] 仅在第一次重置时强制对齐随机数种子
	if g.isFirstReset {
		g.trafficRng = config.NewRand(1)
		g.csmaRng = config.NewRand(2)
		g.isFirstReset = false
		log.Println("🚦 [流量生成器] 首次重置：随机数种子已强制对齐。")
	} else {
		log.Println("🚦 [流量生成器] 后续重置：保留随机数状态以增加多样性。")
	}

	g.Start()
}

// [新增] GetCurrentStatus 返回当前的流量模式和生成间隔
func (g *TrafficGenerator) GetCurrentStatus() (TrafficMode, time.Duration) {
	g.modeMutex.RLock()
	defer g.modeMutex.RUnlock()
	return g.currentMode, g.currentInterval
}

// [大幅修改] setTickerForCurrentMode 根据当前流量模式设置消息生成速率
func (g *TrafficGenerator) setTickerForCurrentMode(ticker **time.Ticker) {
	g.modeMutex.Lock() // [修改] 改为写锁，因为要更新 currentInterval
	defer g.modeMutex.Unlock()

	if *ticker != nil {
		(*ticker).Stop()
	}

	var baseInterval time.Duration
	var jitterRange time.Duration

	switch g.currentMode {
	case ModeBurst: // [新] 突发模式，流量极度密集
		baseInterval = 220 * time.Millisecond * config.BaseIntervalMultiple
		jitterRange = 80 * time.Millisecond // 间隔在 40ms ~ 70ms
	case ModePeak: // 高峰期，非常密集
		baseInterval = 300 * time.Millisecond * config.BaseIntervalMultiple
		jitterRange = 100 * time.Millisecond // 间隔在 150ms ~ 250ms
	case ModeStable: // 平稳期，中等密度
		baseInterval = 800 * time.Millisecond * config.BaseIntervalMultiple
		jitterRange = 400 * time.Millisecond // 间隔在 600ms ~ 1000ms
	case ModeLow: // 低谷期，非常稀疏
		baseInterval = 3 * time.Second
		jitterRange = 2 * time.Second // 间隔在 3s ~ 5s
	}

	// [修改] 使用 trafficRng
	randomJitter := time.Duration(g.trafficRng.Int63n(int64(jitterRange)))
	interval := baseInterval + randomJitter

	// [新增] 更新当前间隔
	g.currentInterval = interval

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

// attemptSendCSMA 使用 P-坚持 CSMA 算法发送消息
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

	// [修改] 使用 csmaRng
	if g.csmaRng.Float64() < p {
		time.Sleep(time.Duration(10+g.csmaRng.Intn(41)) * time.Microsecond)
		transmitted := g.commsSystem.PrimaryChannel.AttemptTransmit(msg, g.ID, 200*time.Millisecond)
		if transmitted {
			g.messageQueue = g.messageQueue[1:]
		}
	}
}
