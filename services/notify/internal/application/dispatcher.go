// 投递决策器（docs/10 §4.1）：渠道偏好 → 熔断/额度检查 → 通道发送 → send_log 落库。
// 决策规则：
//
//	优先短信：allow_sms + 熔断全过 → 短信+邮件（邮件永远兜底）；任一不过 → 仅邮件（DEGRADED）
//	优先邮件：仅邮件；重大振幅（DetailJson.changeRate 绝对值 >= MajorPct）尝试补发短信
package application

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	hqv1 "github.com/hqpush/gate/packages/contract/go/hq/v1"

	"github.com/hqpush/gate/packages/obs"
	"github.com/hqpush/gate/packages/secretx"
	"github.com/hqpush/gate/services/notify/internal/channel"
)

// DispatchStore 决策器持久化接口（MySQL 适配实现）。
type DispatchStore interface {
	VipByID(ctx context.Context, userID int64) (*VipRow, error)
	EmailOfUser(ctx context.Context, userID int64) (string, error)
	InsertSendLogs(ctx context.Context, logs []*SendLogRow) error
	DeveloperByUser(ctx context.Context, userID int64) (*DeveloperRow, error)
}

// DeveloperRow 开发者推送配置（api_developer_config，docs/10 §6 L3 开放层）。
type DeveloperRow struct {
	PushMode  string // ws|webhook
	HookURL   string
	AppSecret string // HMAC 签名密钥（出站 X-Hook-Sign）
}

// VipRow 套餐查询结果（user_vip）。
type VipRow struct {
	PlanType        string
	AllowSMS        bool
	ChannelDefault  string // sms|email|dd|feishu
	DingtalkWebhook string // 钉钉群机器人绑定：URL[|secret][|keyword]（docs/10 §4 IM 渠道）
	FeishuWebhook   string // 飞书群机器人绑定
}

// SendLogRow 发送记录（notify_send_log）。
type SendLogRow struct {
	UserID        int64
	RuleID        int64
	EventID       string
	Channel       string // mail|sms
	Provider      string
	Recipient     string
	ProviderMsgID string
	Status        string // SENT|FAIL
	Error         string
}

// DispatchConfig 决策器配置。
type DispatchConfig struct {
	MajorPct float64 // 重大振幅阈值（百分比），优先邮件模式下触发短信补发
}

// Dispatcher 渠道投递决策器。sms/mail 为 nil 时用 unavailable 占位（恒降级）。
type Dispatcher struct {
	store   DispatchStore
	mail    channel.Sender
	sms     channel.Sender
	dd      channel.Sender // 钉钉群机器人（docs/10 §4 IM 渠道）
	feishu  channel.Sender // 飞书群机器人
	breaker *channel.CircuitBreaker
	cfg     DispatchConfig
	log     *slog.Logger

	sent   *prometheus.CounterVec
	failed *prometheus.CounterVec
	degrad *prometheus.CounterVec
}

func NewDispatcher(store DispatchStore, mail, sms channel.Sender, dd, feishu channel.Sender,
	breaker *channel.CircuitBreaker, cfg DispatchConfig, log *slog.Logger, m *obs.Metrics) *Dispatcher {
	if mail == nil {
		mail = unavailable{name: "mail"}
	}
	if sms == nil {
		sms = unavailable{name: "sms"}
	}
	return &Dispatcher{
		store: store, mail: mail, sms: sms, dd: dd, feishu: feishu,
		breaker: breaker, cfg: cfg, log: log,
		sent:   m.Counter("notify_channel_sent_total", "通道发送成功数", "channel"),
		failed: m.Counter("notify_channel_failed_total", "通道发送失败数", "channel"),
		degrad: m.Counter("notify_channel_degraded_total", "短信降级邮件次数", "reason"),
	}
}

// Dispatch 对单用户单事件执行渠道决策与投递；渠道失败仅记日志/落库，
// 不向上返回错误（告警 WS 主链路不因渠道失败回滚，WS 投递由 InboxService 负责）。
// preferOverride：订阅级渠道偏好（alert_rule.channel_prefer，docs/10 §3.3），
// 空 = 继承 user_vip.channel_default。
func (d *Dispatcher) Dispatch(ctx context.Context, delivery *hqv1.AlertDelivery, ruleID int64, preferOverride string) {
	vip, err := d.store.VipByID(ctx, delivery.UserId)
	if err != nil {
		d.log.Error("dispatch query vip failed", "user_id", delivery.UserId, "err", err)
		return
	}
	if vip == nil { // 无套餐记录：默认免费口径，仅邮件
		vip = &VipRow{PlanType: "free", ChannelDefault: "email"}
	}
	prefer := vip.ChannelDefault
	switch preferOverride { // 订阅级偏好覆盖用户默认（仅白名单值）
	case "sms", "email", "dd", "feishu":
		prefer = preferOverride
	}
	to, err := d.store.EmailOfUser(ctx, delivery.UserId)
	if err != nil {
		d.log.Error("dispatch query email failed", "user_id", delivery.UserId, "err", err)
		return
	}

	// 开发者 webhook 推送（docs/10 §6.3，B16-B 接线）：push_mode=webhook 且 hook_url 非空时
	// 独立于用户渠道偏好投递（开发者同时保留邮件等用户通道）。
	d.maybeWebhook(ctx, delivery, ruleID)

	smsAllowed := vip.AllowSMS && d.breaker.Allow(ctx, "sms", delivery.UserId)
	mailAllowed := d.breaker.Allow(ctx, "mail", delivery.UserId)

	msg := channel.OutboundMessage{
		UserID:  delivery.UserId,
		EventID: delivery.EventId,
		RuleID:  ruleID,
		Subject: fmt.Sprintf("【行情告警】%s %s", delivery.Market.String(), delivery.Symbol),
		Body: fmt.Sprintf("触发时间：%s\n标题：%s\n详情：%s\n",
			time.UnixMilli(delivery.TriggerTs).Format("2006-01-02 15:04:05"),
			delivery.Title, delivery.DetailJson),
		Recipient: to,
	}

	switch prefer {
	case "sms":
		if smsAllowed {
			d.send(ctx, "sms", d.sms, msg, true)
			d.send(ctx, "mail", d.mail, msg, mailAllowed) // 邮件同步备份留存
		} else {
			reason := "sms_quota"
			if !vip.AllowSMS {
				reason = "plan_denied"
			}
			d.degrad.WithLabelValues(reason).Inc()
			d.send(ctx, "mail", d.mail, msg, mailAllowed) // 自动降级：仅邮件
		}
	case "dd", "feishu": // IM 群机器人（钉钉/飞书）：用户绑定 webhook，未绑定自动降级邮件
		bind := vip.DingtalkWebhook
		if prefer == "feishu" {
			bind = vip.FeishuWebhook
		}
		im := d.imSender(prefer, bind)
		if bind == "" || !d.breaker.Allow(ctx, prefer, delivery.UserId) {
			d.degrad.WithLabelValues("im_unbound_or_quota").Inc()
			d.send(ctx, "mail", d.mail, msg, mailAllowed)
			return
		}
		imDispatch := imMsg(msg)
		imDispatch.Recipient = bind // IM 出站目标 = 用户绑定 webhook（修复：原实现误用邮箱）
		d.send(ctx, prefer, im, imDispatch, true)
		d.send(ctx, "mail", d.mail, msg, mailAllowed) // 邮件同步备份留存
	default: // 优先邮件（含默认值）
		d.send(ctx, "mail", d.mail, msg, mailAllowed)
		if majorRate(delivery.DetailJson) >= d.cfg.MajorPct && smsAllowed {
			d.send(ctx, "sms", d.sms, msg, true) // 重大振幅补发短信
		}
	}
}

// maybeWebhook 开发者 webhook 投递：签名 POST 事件 JSON 到 hook_url（2xx 成功，失败仅落库）。
// 事件契约（docs/10 §6.2/§6.3）：eventType=alarm_event + eventId/ruleId/userId/market/symbol/title/detail/triggerTs。
func (d *Dispatcher) maybeWebhook(ctx context.Context, delivery *hqv1.AlertDelivery, ruleID int64) {
	dev, err := d.store.DeveloperByUser(ctx, delivery.UserId)
	if err != nil || dev == nil || dev.PushMode != "webhook" || dev.HookURL == "" {
		return
	}
	url, err := secretx.ValidateBaseURL(dev.HookURL) // 出站 URL 结构化 SSRF 校验（docs/11 §7.2）
	if err != nil {
		d.log.Warn("developer hook_url rejected", "user_id", delivery.UserId, "err", err)
		return
	}
	detail := json.RawMessage(delivery.DetailJson)
	if !json.Valid(detail) {
		detail = json.RawMessage("null")
	}
	body, err := json.Marshal(map[string]any{
		"eventType": "alarm_event",
		"eventId":   delivery.EventId,
		"ruleId":    ruleID,
		"userId":    delivery.UserId,
		"market":    delivery.Market.String(),
		"symbol":    delivery.Symbol,
		"title":     delivery.Title,
		"detail":    detail,
		"triggerTs": delivery.TriggerTs,
	})
	if err != nil {
		return
	}
	d.send(ctx, "webhook", channel.NewWebhookSender(channel.WebhookTarget{URL: url, AppSecret: dev.AppSecret}),
		channel.OutboundMessage{
			UserID: delivery.UserId, EventID: delivery.EventId, RuleID: ruleID,
			Recipient: url, Body: string(body),
		}, false)
}

// imSender 按渠道返回 IM webhook 发送器；bind 为空返回 unavailable（降级路径处理）。
func (d *Dispatcher) imSender(ch, bind string) channel.Sender {
	if bind == "" {
		return unavailable{name: ch}
	}
	if ch == "dd" {
		if d.dd == nil {
			return unavailable{name: ch}
		}
		return d.dd
	}
	if d.feishu == nil {
		return unavailable{name: ch}
	}
	return d.feishu
}

// imMsg IM 渠道出站：正文即出站文本（Recipient 承载用户绑定 webhook 由 IM 适配器解析）。
func imMsg(msg channel.OutboundMessage) channel.OutboundMessage {
	msg.Subject = "" // IM 无主题概念
	return msg
}

// send 单通道发送 + send_log；成功后 Confirm 熔断计数（仅 count=true 的主通道）。
func (d *Dispatcher) send(ctx context.Context, name string, s channel.Sender,
	msg channel.OutboundMessage, count bool) {
	id, err := s.Send(ctx, msg)
	if err == nil && count {
		d.breaker.Confirm(ctx, name, msg.UserID)
	}
	row := &SendLogRow{
		UserID: msg.UserID, EventID: msg.EventID,
		Channel: name, Provider: s.Name(), Recipient: msg.Recipient,
		ProviderMsgID: id, Status: "SENT",
	}
	if err != nil {
		row.Status, row.Error = "FAIL", err.Error()
		d.failed.WithLabelValues(name).Inc()
	} else {
		d.sent.WithLabelValues(name).Inc()
	}
	if err := d.store.InsertSendLogs(ctx, []*SendLogRow{row}); err != nil {
		d.log.Error("insert send log failed", "event_id", msg.EventID, "err", err)
	}
}

// majorRate 从 DetailJson 提取 changeRate（Flink 触发快照字段，缺省 0）。
func majorRate(detailJSON string) float64 {
	var v struct {
		ChangeRate float64 `json:"changeRate"`
	}
	if err := json.Unmarshal([]byte(detailJSON), &v); err != nil {
		return 0
	}
	if v.ChangeRate < 0 {
		return -v.ChangeRate
	}
	return v.ChangeRate
}

// unavailable 未接入通道的占位实现（恒 ErrChannelUnavailable，触发降级）。
type unavailable struct{ name string }

func (u unavailable) Name() string { return u.name }

func (u unavailable) Send(context.Context, channel.OutboundMessage) (string, error) {
	return "", fmt.Errorf("%w: %s not configured", channel.ErrChannelUnavailable, u.name)
}
