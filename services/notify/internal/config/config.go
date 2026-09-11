package config

import (
	"time"

	"github.com/hqpush/gate/packages/configx"
)

// MailConfig 邮件通道配置（NOTIFY_MAIL_*，未配置则邮件通道降级不可用）。
type MailConfig struct {
	Host     string
	Port     int
	TLSMode  string // ssl(默认)|starttls|plain（B16 通用 SMTP：QQ/163/阿里/腾讯同配置面）
	Username string
	Password string
	Sender   string
}

// SmsConfig 短信通道配置（NOTIFY_SMS_*；B16-B 接线，DB 优先 env 兜底）。
type SmsConfig struct {
	Provider        string // aliyun(实装)|tencent|volc|baidu(预留)
	AccessKeyID     string
	AccessKeySecret string
	SignName        string
	TemplateCode    string
	Endpoint        string
}

// Config notify 配置（前缀 NOTIFY_）。
type Config struct {
	HTTPAddr       string
	KafkaBrokers   []string
	RedisAddr      string
	RedisPass      string
	MySQLDSN       string
	GroupID        string
	RetryGroupID   string
	MaxRetry       int
	ExpireInterval time.Duration // 超窗 EXPIRED 扫描周期
	InternalToken  string        // 内部 API 共享令牌（biz-service/ws-gateway 调用）
	NotifyAddr     string        // 对外暴露的内部 API 地址（重试消费地址）
	HookSecret     string        // 服务商回调 HMAC 密钥（生产必须；空则 dev 允许内部 token）

	Mail MailConfig // 邮件通道（docs/10 §4.2）
	Sms  SmsConfig // 短信通道（B16-B 接线）

	MailDailyLimit     int64   // 全局邮件日额度（0 不限，docs/10 §4.3）
	SmsDailyLimit      int64   // 全局短信日额度（0 不限；短信适配器 U2 接入）
	SmsUserDailyLimit  int64   // 单用户短信日上限
	MailUserDailyLimit int64   // 单用户邮件日上限
	MajorPct           float64 // 重大振幅阈值（优先邮件时补发短信）

	SendLogRetention  time.Duration // notify_send_log 保留期（数据生命周期，docs/04 §6）
	CleanupInterval   time.Duration // 清理扫描周期
	CleanupBatchLimit int           // 单批删除上限
}

func Load() Config {
	e := configx.New("NOTIFY")
	return Config{
		HTTPAddr:       e.Str("HTTP_ADDR", ":23023"),
		KafkaBrokers:   e.List("KAFKA_BROKERS", []string{"127.0.0.1:23003"}),
		RedisAddr:      e.Str("REDIS_ADDR", "127.0.0.1:23002"),
		RedisPass:      e.Str("REDIS_PASSWORD", ""),
		MySQLDSN:       e.Str("MYSQL_DSN", "hqpush:hqpush@tcp(127.0.0.1:23001)/hqpush?parseTime=true&loc=Local"),
		GroupID:        e.Str("GROUP_ID", "notify"),
		RetryGroupID:   e.Str("RETRY_GROUP_ID", "notify-retry"),
		MaxRetry:       e.Int("MAX_RETRY", 5),
		ExpireInterval: e.Duration("EXPIRE_INTERVAL", 10*time.Minute),
		InternalToken:  e.Str("INTERNAL_TOKEN", "dev-internal-token"),
		NotifyAddr:     e.Str("NOTIFY_ADDR", "http://127.0.0.1:23023"),
		HookSecret:     e.Str("HOOK_SECRET", ""),

		Mail: MailConfig{
			Host:     e.Str("MAIL_HOST", ""),
			Port:     e.Int("MAIL_PORT", 465),
			TLSMode:  e.Str("MAIL_TLS_MODE", ""),
			Username: e.Str("MAIL_USERNAME", ""),
			Password: e.Str("MAIL_PASSWORD", ""),
			Sender:   e.Str("MAIL_SENDER", ""),
		},

		Sms: SmsConfig{
			Provider:        e.Str("SMS_PROVIDER", ""),
			AccessKeyID:     e.Str("SMS_AK", ""),
			AccessKeySecret: e.Str("SMS_SK", ""),
			SignName:        e.Str("SMS_SIGN", ""),
			TemplateCode:    e.Str("SMS_TEMPLATE", ""),
			Endpoint:        e.Str("SMS_ENDPOINT", ""),
		},

		MailDailyLimit:     e.Int64("MAIL_DAILY_LIMIT", 0),
		SmsDailyLimit:      e.Int64("SMS_DAILY_LIMIT", 0),
		SmsUserDailyLimit:  e.Int64("SMS_USER_DAILY_LIMIT", 0),
		MailUserDailyLimit: e.Int64("MAIL_USER_DAILY_LIMIT", 0),
		MajorPct:           e.Float64("MAJOR_PCT", 9.8),

		SendLogRetention:  e.Duration("SEND_LOG_RETENTION", 90*24*time.Hour),
		CleanupInterval:   e.Duration("CLEANUP_INTERVAL", time.Hour),
		CleanupBatchLimit: e.Int("CLEANUP_BATCH", 1000),
	}
}
