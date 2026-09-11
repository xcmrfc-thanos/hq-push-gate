package config

import (
	"os"
	"strings"
	"time"

	"github.com/hqpush/gate/packages/configx"
)

// Config ws-gateway 配置（前缀 WS_GATEWAY_）。
type Config struct {
	HTTPAddr      string
	KafkaBrokers  []string
	RedisAddr     string
	RedisPass     string
	GatewayID     string
	Slots         []uint32 // 本实例租约持有的 ws_push 槽位
	JWTSecret     string
	NotifyURL     string // notify 内部 API（ACK 回调）
	InternalToken string

	LeaseTTL   time.Duration // 槽位租约 TTL
	LeaseRenew time.Duration // 租约续期周期
	MaxSubs    int           // 单连接订阅上限（冻结契约：200）
	FrameRate  int           // 单连接帧速率上限（冻结契约：20/s）
	DrainGrace time.Duration // drain 优雅退出等待

	EntitleRecheck time.Duration // B22 套餐复核周期（ADR-040：5min）
}

func Load() Config {
	e := configx.New("WS_GATEWAY")
	gwID := e.Str("GATEWAY_ID", "")
	if gwID == "" {
		h, _ := os.Hostname()
		gwID = "ws-" + strings.ToLower(h)
	}
	return Config{
		HTTPAddr:       e.Str("HTTP_ADDR", ":23024"),
		KafkaBrokers:   e.List("KAFKA_BROKERS", []string{"127.0.0.1:23003"}),
		RedisAddr:      e.Str("REDIS_ADDR", "127.0.0.1:23002"),
		RedisPass:      e.Str("REDIS_PASSWORD", ""),
		GatewayID:      gwID,
		Slots:          e.Uint32List("SLOTS", []uint32{0}),
		JWTSecret:      e.Str("JWT_SECRET", "dev-jwt-secret"),
		NotifyURL:      e.Str("NOTIFY_URL", "http://127.0.0.1:23023"),
		InternalToken:  e.Str("INTERNAL_TOKEN", "dev-internal-token"),
		LeaseTTL:       e.Duration("LEASE_TTL", 15*time.Second),
		LeaseRenew:     e.Duration("LEASE_RENEW", 5*time.Second),
		MaxSubs:        e.Int("MAX_SUBS", 200),
		FrameRate:      e.Int("FRAME_RATE", 20),
		DrainGrace:     e.Duration("DRAIN_GRACE", 10*time.Second),
		EntitleRecheck: e.Duration("ENTITLE_RECHECK", 5*time.Minute),
	}
}
