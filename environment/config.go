package environment

// Config 结构体用于封装所有可以从外部配置的环境参数。
// 这样做可以使 NewServer 的函数签名保持整洁，并易于未来扩展。
type Config struct {
	EnableDualChannel bool
	// 未来可以添加更多配置，例如:
	// NumAircraft int
	// TotalSimDuration time.Duration
}