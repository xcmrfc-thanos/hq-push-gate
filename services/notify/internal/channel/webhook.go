// WebHook 投递适配器（docs/10 §6 L3 开放层）：HMAC 签名 + 有限退避重试 + 失败落库不阻塞。
// 开发者配置 api_developer_config：push_mode=webhook 时告警事件 POST 到 hook_url。
// 签名头：X-Hook-Sign = hex(hmac_sha256(app_secret, body))；X-Event-Type = alarm_event。
package channel

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// WebhookTarget 开发者回调目标。
type WebhookTarget struct {
	URL       string // HTTPS 回调地址
	AppSecret string // HMAC 签名密钥
}

// webhookHTTP 包级共享出站 client：复用连接池（每次请求新建 client 会丢 keep-alive，
// B17 性能修正：连接池口径见 docs/12 §1.1）。
var webhookHTTP = &http.Client{Timeout: 10 * time.Second}

// SendWebhook 签名投递（v2，docs/11 §5.3）：签名覆盖 ts+nonce+body 防重放，X-Hook-Version=2；
// 2xx 视为成功。签名 = HEX(HMAC-SHA256(app_secret, ts + "." + nonce + "." + SHA256_HEX(body)))。
func SendWebhook(ctx context.Context, target WebhookTarget, body []byte) error {
	if !strings.HasPrefix(target.URL, "https://") {
		return fmt.Errorf("%w: webhook url must be https", ErrChannelUnavailable)
	}
	ts := fmt.Sprintf("%d", time.Now().UnixMilli())
	nonce := newNonce()
	bodySum := sha256.Sum256(body)
	mac := hmac.New(sha256.New, []byte(target.AppSecret))
	mac.Write([]byte(ts + "." + nonce + "." + hex.EncodeToString(bodySum[:])))
	sig := hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hook-Sign", "sha256="+sig)
	req.Header.Set("X-Hook-Timestamp", ts)
	req.Header.Set("X-Hook-Nonce", nonce)
	req.Header.Set("X-Hook-Version", "2")
	req.Header.Set("X-Event-Type", "alarm_event")

	resp, err := webhookHTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook http %d", resp.StatusCode)
	}
	return nil
}

// newNonce 一次性随机数（16 字节 hex）。
func newNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// WebhookSender 开发者 webhook 投递 Sender（B16-B 接线）：msg.Body 承载已序列化的事件 JSON，
// msg.Recipient 承载 hook_url（落库对账用）。
type WebhookSender struct {
	target WebhookTarget
}

func NewWebhookSender(target WebhookTarget) *WebhookSender {
	return &WebhookSender{target: target}
}

func (s *WebhookSender) Name() string { return "webhook" }

func (s *WebhookSender) Send(ctx context.Context, msg OutboundMessage) (string, error) {
	if s.target.URL == "" || s.target.AppSecret == "" {
		return "", fmt.Errorf("%w: webhook target empty", ErrChannelUnavailable)
	}
	if err := SendWebhook(ctx, s.target, []byte(msg.Body)); err != nil {
		return "", err
	}
	return "webhook:" + msg.EventID, nil
}
