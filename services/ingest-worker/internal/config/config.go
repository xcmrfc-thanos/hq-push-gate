package config

import (
	"github.com/hqpush/gate/packages/configx"
)

// Config ingest-worker 配置（前缀 INGEST_）。
type Config struct {
	KafkaBrokers  []string
	GroupID       string
	ClickHouseDSN string // 为空则跳过 CK 写入（纯实时演示模式，docs/08 §3.1）
	DorisDSN      string // 为空则关闭 alert_event 归档（U0 默认关闭 Doris）
	BatchSize     int
	FlushInterval int64 // 毫秒
}

func Load() Config {
	e := configx.New("INGEST")
	return Config{
		KafkaBrokers:  e.List("KAFKA_BROKERS", []string{"127.0.0.1:23003"}),
		GroupID:       e.Str("GROUP_ID", "ingest-worker"),
		ClickHouseDSN: e.Str("CLICKHOUSE_DSN", "clickhouse://127.0.0.1:23005/default?username=hqpush&password=hqpush"),
		DorisDSN:      e.Str("DORIS_DSN", ""),
		BatchSize:     e.Int("BATCH_SIZE", 5000), // CK 大批量写入基线（ADR-017：每 5k 行或 1s）
		FlushInterval: e.Int64("FLUSH_INTERVAL_MS", 1000),
	}
}
