package config

import (
	"github.com/hqpush/gate/packages/configx"
)

// Config tick 模拟源配置（前缀 TICK_SOURCE_）。
type Config struct {
	GatewayURL  string  // hq-gateway 接入地址
	Markets     []string
	Symbols     int     // 每市场标的数
	Rate        float64 // 每秒 tick 数
	Duration    int64   // 运行秒数，0 为持续
	BatchSize   int
}

func Load() Config {
	e := configx.New("TICK_SOURCE")
	return Config{
		GatewayURL: e.Str("GATEWAY_URL", "http://127.0.0.1:23021"),
		Markets:    e.List("MARKETS", []string{"A_SHARE"}),
		Symbols:    e.Int("SYMBOLS", 20),
		Rate:       float64(e.Int64("RATE", 100)),
		Duration:   e.Int64("DURATION", 0),
		BatchSize:  e.Int("BATCH_SIZE", 100),
	}
}
