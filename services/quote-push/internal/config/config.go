package config

import (
	"time"

	"github.com/hqpush/gate/packages/configx"
)

// Config quote-push 配置（前缀 QUOTE_PUSH_）。
type Config struct {
	HTTPAddr      string
	KafkaBrokers  []string
	RedisAddr     string
	RedisPass     string
	GroupID       string
	MergeWindow   time.Duration // 行情合并窗口（丢旧保新）
	CommitInterval time.Duration // tick offset 批量提交周期（docs/12 U2 性能项；崩溃回放≤1 周期）
}

func Load() Config {
	e := configx.New("QUOTE_PUSH")
	return Config{
		HTTPAddr:       e.Str("HTTP_ADDR", ":23022"),
		KafkaBrokers:   e.List("KAFKA_BROKERS", []string{"127.0.0.1:23003"}),
		RedisAddr:      e.Str("REDIS_ADDR", "127.0.0.1:23002"),
		RedisPass:      e.Str("REDIS_PASSWORD", ""),
		GroupID:        e.Str("GROUP_ID", "quote-push"),
		MergeWindow:    e.Duration("MERGE_WINDOW", 200*time.Millisecond),
		CommitInterval: e.Duration("COMMIT_INTERVAL", time.Second),
	}
}
