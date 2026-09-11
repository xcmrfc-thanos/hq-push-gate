package config

import (
	"time"

	"github.com/hqpush/gate/packages/configx"
)

// Config hq-gateway 配置（前缀 HQ_GATEWAY_）。
type Config struct {
	HTTPAddr      string
	KafkaBrokers  []string
	MaxBatchTicks int

	// 内置行情源选择：off（默认，仅 HTTP 被动接入）/ sim（内置模拟源）/ eastmoney（东财免费快照）。
	Source string

	SimRate    float64 // sim：每秒批次
	SimBatch   int     // sim：每批 tick 数
	SimSymbols int     // sim：模拟标的数量

	EastInterval   time.Duration // eastmoney：轮询周期
	EastMinGap     time.Duration // eastmoney：相邻请求最小间隔（频控）
	EastBackoff    time.Duration // eastmoney：退避基值
	EastBackoffMax time.Duration // eastmoney：退避封顶
	EastMaxBatch   int           // eastmoney：单请求 secid 上限
	EastSymbols    []string      // eastmoney：标的列表（全市场发现与 symbol_meta 入库在 kline-history 分支）
	EastSession    bool          // eastmoney：仅在 A 股交易时段轮询（默认关，便于收盘后联调）
	InternalToken  string        // /internal/bench/* 内部令牌
}

func Load() Config {
	e := configx.New("HQ_GATEWAY")
	return Config{
		HTTPAddr:      e.Str("HTTP_ADDR", ":23021"),
		KafkaBrokers:  e.List("KAFKA_BROKERS", []string{"127.0.0.1:23003"}),
		MaxBatchTicks: e.Int("MAX_BATCH_TICKS", 5000),

		Source: e.Str("SOURCE", "off"),

		SimRate:    e.Float64("SIM_RATE", 5),
		SimBatch:   e.Int("SIM_BATCH", 2),
		SimSymbols: e.Int("SIM_SYMBOLS", 8),

		EastInterval:   e.Duration("EAST_INTERVAL", 3*time.Second),
		EastMinGap:     e.Duration("EAST_MIN_GAP", 500*time.Millisecond),
		EastBackoff:    e.Duration("EAST_BACKOFF", time.Second),
		EastBackoffMax: e.Duration("EAST_BACKOFF_MAX", 5*time.Minute),
		EastMaxBatch:   e.Int("EAST_MAX_BATCH", 80),
		EastSymbols:    e.List("EAST_SYMBOLS", []string{"600000", "000001", "300750", "600519", "601318", "000858"}),
		EastSession:    e.Bool("EAST_SESSION", false),
		InternalToken:  e.Str("INTERNAL_TOKEN", "dev-internal-token"),
	}
}
