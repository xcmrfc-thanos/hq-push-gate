// SMTP 邮件适配器：标准 net/smtp + 隐式 TLS（阿里云 DirectMail smtpdm.aliyun.com:465 同协议）。
// 凭据仅 env（NOTIFY_MAIL_*），未配置时所有 Send 返回 ErrChannelUnavailable（触发降级）。
// 邮件正文统一附加风险提示与退订说明（docs/10 §9 合规）。
package channel

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

const (
	mailRiskFooter = "\n\n---\n风险提示：股市有风险，投资需谨慎。本邮件由系统自动发送，不构成投资建议。\n" +
		"如需退订，请在邮件内点击“取消订阅”，或将本邮件地址加入过滤。"
	maxSubjectLen = 100
)

// MailConfig 邮件通道配置：通用 SMTP 协议面（QQ smtp.qq.com:465 / 163 smtp.163.com:465 /
// 阿里 smtpdm / 腾讯 SES 同配置面，docs/12 B16 多渠道邮件）。TLSMode: ssl(默认,隐式 TLS)|
// starttls(587)|plain。
type MailConfig struct {
	Host     string // SMTP 主机
	Port     int    // 465 / 587 / 25
	TLSMode  string // ssl|starttls|plain（空 = ssl）
	Username string // 发信账号
	Password string // SMTP 密码/授权码（DB 密文或 env 注入）
	Sender   string // 显示发件人
}

func (c MailConfig) tlsMode() string {
	if c.TLSMode == "" {
		return "ssl"
	}
	return c.TLSMode
}

func (c MailConfig) configured() bool {
	return c.Host != "" && c.Port > 0 && c.Username != "" && c.Password != "" && c.Sender != ""
}

// MailLookup 邮件配置热加载源（ConfigProvider 实现）；false = 无 DB 配置，用 fallback。
type MailLookup interface {
	Mail(ctx context.Context) (MailConfig, bool)
}

// MailSender SMTP 发送器：prov 非空时每次 Send 热取配置（30s 缓存），否则用静态 fallback。
type MailSender struct {
	prov     MailLookup
	fallback MailConfig
}

func NewMailSender(cfg MailConfig) *MailSender { return &MailSender{fallback: cfg} }

// NewDynamicMailSender DB 配置优先（notify_channel 密文表热加载），env 值作 fallback。
func NewDynamicMailSender(prov MailLookup, fallback MailConfig) *MailSender {
	return &MailSender{prov: prov, fallback: fallback}
}

func (s *MailSender) Name() string { return "mail" }

func (s *MailSender) Send(ctx context.Context, msg OutboundMessage) (string, error) {
	cfg := s.fallback
	if s.prov != nil {
		if c, ok := s.prov.Mail(ctx); ok {
			cfg = c
		}
	}
	if !cfg.configured() {
		return "", ErrChannelUnavailable
	}
	if msg.Recipient == "" {
		return "", fmt.Errorf("mail recipient empty")
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	subject := msg.Subject
	if len([]rune(subject)) > maxSubjectLen {
		subject = string([]rune(subject)[:maxSubjectLen]) + "..."
	}
	body := strings.TrimRight(msg.Body, "\n") + mailRiskFooter

	// RFC 5322 头；主题 Q 编码规避非 ASCII 头注入（\r\n 已由 sanitize 移除）
	header := map[string]string{
		"From":         cfg.Sender,
		"To":           msg.Recipient,
		"Subject":      "=?UTF-8?B?" + b64String(subject) + "?=",
		"MIME-Version": "1.0",
		"Content-Type": `text/plain; charset="UTF-8"`,
	}
	var sb strings.Builder
	for _, k := range []string{"From", "To", "Subject", "MIME-Version", "Content-Type"} {
		sb.WriteString(k + ": " + sanitizeHeader(header[k]) + "\r\n")
	}
	sb.WriteString("\r\n" + body)

	// 头部构造完毕后按 TLSMode 建连；正文统一附加风险提示与退订说明
	auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
	var conn net.Conn
	var err error
	switch cfg.tlsMode() {
	case "starttls": // 587：明文连接后 STARTTLS 升级
		conn, err = net.DialTimeout("tcp", addr, 10*time.Second)
	case "plain": // 内网中继等明文场景
		conn, err = net.DialTimeout("tcp", addr, 10*time.Second)
	default: // ssl：隐式 TLS（465）
		conn, err = tls.Dial("tcp", addr, &tls.Config{ServerName: cfg.Host})
	}
	if err != nil {
		return "", fmt.Errorf("smtp dial: %w", err)
	}
	defer conn.Close()
	cli, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return "", fmt.Errorf("smtp client: %w", err)
	}
	defer cli.Close()
	if cfg.tlsMode() == "starttls" {
		if err = cli.StartTLS(&tls.Config{ServerName: cfg.Host}); err != nil {
			return "", fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if err = cli.Auth(auth); err != nil {
		return "", fmt.Errorf("smtp auth: %w", err)
	}
	if err = cli.Mail(cfg.Username); err != nil {
		return "", fmt.Errorf("smtp mail from: %w", err)
	}
	if err = cli.Rcpt(msg.Recipient); err != nil {
		return "", fmt.Errorf("smtp rcpt: %w", err)
	}
	w, err := cli.Data()
	if err != nil {
		return "", fmt.Errorf("smtp data: %w", err)
	}
	if _, err = w.Write([]byte(sb.String())); err != nil {
		return "", fmt.Errorf("smtp write: %w", err)
	}
	if err = w.Close(); err != nil {
		return "", fmt.Errorf("smtp data close: %w", err)
	}
	if err = cli.Quit(); err != nil {
		return "", fmt.Errorf("smtp quit: %w", err)
	}
	// providerMsgID：SMTP 无异步 ID，用收件地址对账（回执按 toAddress 反查）
	return "smtp:" + msg.Recipient, nil
}

// sanitizeHeader 移除头注入向量（CRLF）。
func sanitizeHeader(v string) string {
	v = strings.ReplaceAll(v, "\r", "")
	return strings.ReplaceAll(v, "\n", "")
}

func b64String(s string) string {
	const enc = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	in := []byte(s)
	var out strings.Builder
	for i := 0; i < len(in); i += 3 {
		var chunk [3]byte
		n := copy(chunk[:], in[i:])
		var v uint32
		for j := 0; j < n; j++ {
			v |= uint32(chunk[j]) << (8 * (2 - j))
		}
		for j := 0; j < n+1; j++ {
			out.WriteByte(enc[(v>>(18-6*j))&0x3F])
		}
		for j := n; j < 3; j++ {
			out.WriteByte('=')
		}
	}
	return out.String()
}
