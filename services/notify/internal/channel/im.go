// IM webhook 适配器（钉钉/飞书群机器人）：运维与行情告警的即时可达通道。
//
// 绑定方式（用户端）：群内添加自定义机器人 → 取 webhook URL 中的 secret（钉钉加签/飞书加签）
// → 在本系统绑定（user_vip.dingtalk_webhook / feishu_webhook）。
//
// 出站协议差异（均为 HTTPS POST JSON）：
//
//	钉钉: {"msgtype":"text","text":{"content":"..."}} + 加签参数 timestamp&sign（HMAC-SHA256）
//	飞书: {"msg_type":"text","content":{"text":"..."}} + 加签参数 timestamp&sign（HMAC-SHA256）
//
// 限流约定：单个机器人 ~20 条/min（服务商侧限制），由 CircuitBreaker rl:dd/rl:feishu 日额度控制。
package channel

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// webhookTarget 出站目标（用户绑定信息）。
type webhookTarget struct {
	URL     string // 用户提供的机器人 webhook 地址
	Secret  string // 加签密钥（钉钉/飞书可选开启安全设置）
	Keyword string // 自定义关键词安全设置：content 必须包含该词（钉钉/飞书通用）
}

type imWebhookSender struct {
	client  *http.Client
	channel string // dd|feishu
}

func newIMWebhookSender(channelName string) *imWebhookSender {
	return &imWebhookSender{
		client:  &http.Client{Timeout: 10 * time.Second},
		channel: channelName,
	}
}

// IMSender 钉钉/飞书群机器人统一 Sender（docs/10 §4 IM 渠道）：
// msg.Recipient 承载用户绑定值 URL|secret|keyword，msg.Body 为出站文本。
type IMSender struct {
	inner *imWebhookSender
}

// NewIMSender 构造指定渠道（dd|feishu）的 IM 发送器（B16-B 接线：凭据是用户级绑定，无需平台凭据）。
func NewIMSender(channelName string) *IMSender {
	return &IMSender{inner: newIMWebhookSender(channelName)}
}

func (s *IMSender) Name() string { return s.inner.channel }

func (s *IMSender) Send(ctx context.Context, msg OutboundMessage) (string, error) {
	if msg.Recipient == "" {
		return "", fmt.Errorf("%w: im bind empty", ErrChannelUnavailable)
	}
	if err := s.inner.sendIM(ctx, msg.Recipient, msg.Body); err != nil {
		return "", err
	}
	return s.inner.channel + ":ok", nil
}

// parseTarget 从用户绑定值解析 target：直接存 webhook URL；secret/keyword 可用 | 分隔附加。
// 例：https://oapi.dingtalk.com/robot/send?access_token=xx|SEGRET|关键词
func parseTarget(bound string) (webhookTarget, error) {
	parts := strings.Split(bound, "|")
	t := webhookTarget{URL: strings.TrimSpace(parts[0])}
	if t.URL == "" {
		return t, fmt.Errorf("%w: empty webhook url", ErrChannelUnavailable)
	}
	if !strings.HasPrefix(t.URL, "https://") {
		return t, fmt.Errorf("%w: webhook url must be https", ErrChannelUnavailable)
	}
	if len(parts) > 1 {
		t.Secret = strings.TrimSpace(parts[1])
	}
	if len(parts) > 2 {
		t.Keyword = strings.TrimSpace(parts[2])
	}
	return t, nil
}

// sign 加签（钉钉/飞书同构）：sign = base64(HMAC-SHA256(secret, fmt.Sprintf("%d\n%s", ts, secret)))
func sign(secret string, ts int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d\n%s", ts, secret)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// sendIM 组装出站并 POST；成功判定：HTTP 200 且业务码为 0（钉钉 errcode/飞书 code）。
func (s *imWebhookSender) sendIM(ctx context.Context, bound string, text string) error {
	t, err := parseTarget(bound)
	if err != nil {
		return err
	}
	if t.Keyword != "" && !strings.Contains(text, t.Keyword) {
		text = t.Keyword + " " + text // 自定义关键词安全模式：自动前置关键词
	}
	endpoint := t.URL
	if t.Secret != "" {
		ts := time.Now().UnixMilli()
		sep := "&"
		if !strings.Contains(endpoint, "?") {
			sep = "?"
		}
		endpoint = fmt.Sprintf("%s%stimestamp=%d&sign=%s", endpoint, sep, ts, url.QueryEscape(sign(t.Secret, ts)))
	}

	var payload []byte
	if s.channel == "feishu" {
		payload, err = json.Marshal(map[string]any{
			"msg_type": "text",
			"content":  map[string]string{"text": text},
		})
	} else {
		payload, err = json.Marshal(map[string]any{
			"msgtype": "text",
			"text":    map[string]string{"content": text},
		})
	}
	if err != nil {
		return fmt.Errorf("marshal im payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return fmt.Errorf("im request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("im post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("im http %d", resp.StatusCode)
	}
	// 业务码检查：钉钉 {"errcode":0,...}；飞书 {"code":0,...}（TenantAccessToken 免鉴权的自定义机器人也是 code）
	var biz struct {
		ErrCode int    `json:"errcode"`
		Code    int    `json:"code"`
		ErrMsg  string `json:"errmsg"`
		Msg     string `json:"msg"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&biz)
	if biz.ErrCode != 0 {
		return fmt.Errorf("dingtalk errcode=%d %s", biz.ErrCode, biz.ErrMsg)
	}
	if biz.Code != 0 {
		return fmt.Errorf("feishu code=%d %s", biz.Code, biz.Msg)
	}
	return nil
}
