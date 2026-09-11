// 渠道配置热加载（docs/12 B16-B）：notify_channel 密文表 → MailConfig/SmsConfig。
// 回退链：表空 / MySQL 不可达 / 主密钥未配 → env fallback（NOTIFY_MAIL_* / NOTIFY_SMS_*，config.Load）。
// 加载失败保留上次成功值（宕机窗口内沿用旧配置，行为与"未配置即降级"兼容）。
package channel

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"time"

	"github.com/hqpush/gate/packages/secretx"
)

const channelConfigTTL = 30 * time.Second

// ConfigProvider 渠道配置源（MailLookup / SmsLookup 的 DB 实现）。
type ConfigProvider struct {
	db   *sql.DB
	ring *secretx.KeyRing // nil = 未配主密钥，仅 env 回退
	ttl  time.Duration

	mu       sync.Mutex
	mail     MailConfig
	mailOK   bool
	sms      SmsConfig
	smsOK    bool
	loadedAt time.Time
}

// NewConfigProvider 构造；db/ring 允许 nil（此时恒走 env 回退）。
func NewConfigProvider(db *sql.DB, ring *secretx.KeyRing, ttl time.Duration) *ConfigProvider {
	if ttl <= 0 {
		ttl = channelConfigTTL
	}
	return &ConfigProvider{db: db, ring: ring, ttl: ttl}
}

// Mail 当前生效邮件配置（DB 优先，回退 false → 调用方用 env fallback）。
func (p *ConfigProvider) Mail(ctx context.Context) (MailConfig, bool) {
	p.ensure(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.mail, p.mailOK
}

// SMS 当前生效短信配置。
func (p *ConfigProvider) SMS(ctx context.Context) (SmsConfig, bool) {
	p.ensure(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sms, p.smsOK
}

// Reload 管理端写库后强制失效缓存（下次读取立即回源）。
func (p *ConfigProvider) Reload() {
	p.mu.Lock()
	p.loadedAt = time.Time{}
	p.mu.Unlock()
}

func (p *ConfigProvider) ensure(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if time.Since(p.loadedAt) < p.ttl {
		return
	}
	p.loadedAt = time.Now()
	if p.db == nil || p.ring == nil {
		return
	}
	rows, err := p.db.QueryContext(ctx,
		`SELECT channel, config_enc FROM notify_channel WHERE enabled = 1 ORDER BY id`)
	if err != nil {
		return // 查询失败：保留上次成功配置
	}
	defer rows.Close()
	var mailSeen, smsSeen bool
	for rows.Next() {
		var ch, enc string
		if err := rows.Scan(&ch, &enc); err != nil {
			continue
		}
		plain, err := p.ring.Decrypt(enc)
		if err != nil {
			continue // 单行密文坏：跳过该行
		}
		switch ch {
		case "mail":
			if !mailSeen {
				if c, err := mailConfigFromJSON(plain); err == nil {
					p.mail, p.mailOK, mailSeen = c, true, true
				}
			}
		case "sms":
			if !smsSeen {
				if c, err := smsConfigFromJSON(plain); err == nil {
					p.sms, p.smsOK, smsSeen = c, true, true
				}
			}
		}
	}
}

// ---- JSON 载荷 ----

type mailConfigJSON struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	TLSMode  string `json:"tls_mode"` // ssl|starttls|plain
	Username string `json:"username"`
	Password string `json:"password"`
	Sender   string `json:"sender"`
}

type smsConfigJSON struct {
	Provider  string `json:"provider"` // aliyun|tencent|volc|baidu（后三家预留）
	AccessKey string `json:"ak_id"`
	Secret    string `json:"ak_secret"`
	Sign      string `json:"sign"`
	Template  string `json:"template"`
	Endpoint  string `json:"endpoint"`
}

func mailConfigFromJSON(plain string) (MailConfig, error) {
	var j mailConfigJSON
	if err := json.Unmarshal([]byte(plain), &j); err != nil {
		return MailConfig{}, err
	}
	return MailConfig{
		Host: j.Host, Port: j.Port, TLSMode: j.TLSMode,
		Username: j.Username, Password: j.Password, Sender: j.Sender,
	}, nil
}

func smsConfigFromJSON(plain string) (SmsConfig, error) {
	var j smsConfigJSON
	if err := json.Unmarshal([]byte(plain), &j); err != nil {
		return SmsConfig{}, err
	}
	return SmsConfig{
		Provider: j.Provider, AccessKeyID: j.AccessKey, AccessKeySecret: j.Secret,
		SignName: j.Sign, TemplateCode: j.Template, Endpoint: j.Endpoint,
	}, nil
}
