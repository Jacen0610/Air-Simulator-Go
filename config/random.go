// C:/workspace/go/Air-Simulator-Go/config/random.go
package config

import (
	"log"
	"math/rand"
	"time"
)

// simRand 是一个包级别的、私有的随机数生成器实例。
// 它不应该被直接导出，以强制通过 GetSimRand() 函数来获取，确保它已被正确初始化。
var simRand *rand.Rand

// init 函数会在 config 包被首次导入时自动执行一次。
// 这是进行全局状态（如我们的随机数生成器）初始化的理想位置。
func init() {
	var seed int64
	if UseFixedSeed {
		// 如果开关打开，使用预设的种子值
		seed = RandomSeed
	} else {
		// 否则，使用当前时间作为随机种子
		seed = time.Now().UnixNano()
	}

	// 使用种子创建一个新的随机数源
	source := rand.NewSource(seed)
	// 从源创建一个新的随机数生成器实例
	simRand = rand.New(source)

	log.Printf("🎲 随机数生成器已初始化。固定种子: %v, 种子值: %d", UseFixedSeed, seed)
}

// GetSimRand 返回全局唯一的、已经初始化好的随机数生成器实例。
// 项目中所有需要随机数的地方都应该调用这个函数，而不是使用 math/rand 的全局函数。
func GetSimRand() *rand.Rand {
	return simRand
}
