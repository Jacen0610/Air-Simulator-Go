// C:/workspace/go/Air-Simulator-Go/config/random.go
package config

import (
	"log"
	"math/rand"
	"time"
)

// NewRand 创建并返回一个新的、独立的随机数生成器实例。
// seedOffset 参数用于区分不同的组件或用途，确保它们拥有独立的随机序列。
//
// 例如：
// - TrafficGenerator (流量模式): NewRand(1)
// - TrafficGenerator (CSMA逻辑): NewRand(2)
// - Aircraft (智能体): NewRand(3)
func NewRand(seedOffset int64) *rand.Rand {
	var seed int64
	if UseFixedSeed {
		// 如果开关打开，使用预设的种子值 + 偏移量
		// 这样既保证了可复现性，又保证了不同组件的序列隔离
		seed = RandomSeed + seedOffset
	} else {
		// 否则，使用当前时间 + 偏移量作为种子
		seed = time.Now().UnixNano() + seedOffset
	}

	// 使用种子创建一个新的随机数源
	source := rand.NewSource(seed)
	// 从源创建一个新的随机数生成器实例
	rng := rand.New(source)

	log.Printf("🎲 创建新的随机数生成器 (Offset: %d). 固定种子: %v, 最终种子: %d", seedOffset, UseFixedSeed, seed)
	return rng
}
