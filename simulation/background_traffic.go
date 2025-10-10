package simulation

import (
	"Air-Simulator/config"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"
)

// BackgroundTrafficGenerator simulates other non-agent traffic in the environment.
type BackgroundTrafficGenerator struct {
	ID      string
	channel *Channel
	wg      *sync.WaitGroup
	stopCh  chan struct{}
}

// NewBackgroundTrafficGenerator creates a new traffic generator.
func NewBackgroundTrafficGenerator(id string, channel *Channel, wg *sync.WaitGroup) *BackgroundTrafficGenerator {
	return &BackgroundTrafficGenerator{
		ID:      id,
		channel: channel,
		wg:      wg,
		stopCh:  make(chan struct{}),
	}
}

// Start begins the traffic generation loop. This should be run in a goroutine.
func (g *BackgroundTrafficGenerator) Start() {
	defer g.wg.Done()
	log.Printf("🛰️  背景流量生成器 [%s] 已启动...", g.ID)

	// 定义流量模式，模拟动态变化的环境
	patterns := []struct {
		duration      time.Duration // 此模式的持续时间
		avgInterval   time.Duration // 平均发送间隔
		burstiness    float64       // 间隔的随机变化幅度 (0.0 to 1.0)
		burstSendProb float64       // 在一个发送点位，实际尝试发送的概率
	}{
		{duration: 5 * time.Minute, avgInterval: 8 * time.Second, burstiness: 0.5, burstSendProb: 0.3},        // 平稳期
		{duration: 2 * time.Minute, avgInterval: 1 * time.Second, burstiness: 0.3, burstSendProb: 0.8},        // 高拥塞期
		{duration: 5 * time.Minute, avgInterval: 6 * time.Second, burstiness: 0.5, burstSendProb: 0.4},        // 正常期
		{duration: 2 * time.Minute, avgInterval: 800 * time.Millisecond, burstiness: 0.2, burstSendProb: 0.9}, // 极高拥塞期
		{duration: 6 * time.Minute, avgInterval: 10 * time.Second, burstiness: 0.7, burstSendProb: 0.2},       // 低谷期
	}

	patternTicker := time.NewTicker(patterns[0].duration)
	currentPatternIndex := 0

	currentPattern := patterns[currentPatternIndex]
	sendTicker := time.NewTicker(g.calculateNextSend(currentPattern))

	for {
		select {
		case <-patternTicker.C:
			// 切换到下一个流量模式
			currentPatternIndex = (currentPatternIndex + 1) % len(patterns)
			currentPattern = patterns[currentPatternIndex]
			patternTicker.Reset(currentPattern.duration)
			sendTicker.Reset(g.calculateNextSend(currentPattern))
			log.Printf("🛰️  [%s] 流量模式切换: 平均间隔 %.1fs, 发送概率 %.2f", g.ID, currentPattern.avgInterval.Seconds(), currentPattern.burstSendProb)

		case <-sendTicker.C:
			// 到达发送时间点，根据概率决定是否发送
			if rand.Float64() < currentPattern.burstSendProb {
				go g.attemptSend()
			}
			// 计算并重置下一次发送的时间
			sendTicker.Reset(g.calculateNextSend(currentPattern))

		case <-g.stopCh:
			log.Printf("🛰️  背景流量生成器 [%s] 已停止。", g.ID)
			patternTicker.Stop()
			sendTicker.Stop()
			return
		}
	}
}

// calculateNextSend 根据当前模式计算下一次发送的随机间隔。
func (g *BackgroundTrafficGenerator) calculateNextSend(pattern struct {
	duration      time.Duration
	avgInterval   time.Duration
	burstiness    float64
	burstSendProb float64
}) time.Duration {
	variation := float64(pattern.avgInterval) * pattern.burstiness
	offset := (rand.Float64()*2 - 1) * variation // 产生一个在 [-variation, +variation] 范围内的随机值
	nextInterval := pattern.avgInterval + time.Duration(offset)
	if nextInterval < 50*time.Millisecond { // 防止间隔过小
		nextInterval = 50 * time.Millisecond
	}
	return nextInterval
}

// attemptSend 创建一个虚拟消息并尝试在信道上传输。
func (g *BackgroundTrafficGenerator) attemptSend() {
	// 创建一个简单的虚拟消息，它不需要真实数据
	dummyData := struct{ Info string }{Info: "Background Traffic"}
	baseMsg := ACARSBaseMessage{
		AircraftICAOAddress: "BG_TRAFFIC",
		FlightID:            "BG000",
		MessageID:           fmt.Sprintf("BG-%d", time.Now().UnixNano()),
		Timestamp:           time.Now(),
		Type:                MsgTypeFreeText,
	}
	// 背景流量可以被视为低优先级
	msg, _ := NewLowAuxiliaryPriorityMessage(baseMsg, dummyData)

	// 尝试在信道上传输，不关心结果。这个动作本身就是为了模拟信道占用。
	if !g.channel.IsBusy() {
		log.Printf("🛰️  [%s] 正在尝试发送背景流量...", g.ID)
		g.channel.AttemptTransmit(msg, g.ID, config.TransmissionTime)
	}
}

// Stop 优雅地停止生成器。
func (g *BackgroundTrafficGenerator) Stop() {
	close(g.stopCh)
}
