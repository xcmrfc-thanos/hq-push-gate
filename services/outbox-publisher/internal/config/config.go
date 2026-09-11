package config

import (
	"time"

	"github.com/hqpush/gate/packages/configx"
)

// Config outbox-publisher 配置（前缀 OUTBOX_PUB_）。
type Config struct {
	KafkaBrokers    []string
	MySQLDSN        string
	PollInterval    time.Duration
	BatchSize       int
	Retention       time.Duration // PUBLISHED 事件保留期（数据生命周期，docs/04 §6）
	CleanupInterval time.Duration // 清理扫描周期
	CleanupBatch    int           // 单批删除上限
}

func Load() Config {
	e := configx.New("OUTBOX_PUB")
	return Config{
		KafkaBrokers:    e.List("KAFKA_BROKERS", []string{"127.0.0.1:23003"}),
		MySQLDSN:        e.Str("MYSQL_DSN", "hqpush:hqpush@tcp(127.0.0.1:23001)/hqpush?parseTime=true&loc=Local"),
		PollInterval:    e.Duration("POLL_INTERVAL", 500*time.Millisecond),
		BatchSize:       e.Int("BATCH_SIZE", 100),
		Retention:       e.Duration("RETENTION", 7*24*time.Hour),
		CleanupInterval: e.Duration("CLEANUP_INTERVAL", time.Hour),
		CleanupBatch:    e.Int("CLEANUP_BATCH", 1000),
	}
}
