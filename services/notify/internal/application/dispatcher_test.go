// Dispatcher 单测：渠道偏好决策 / IM 绑定缺失降级 / 熔断降级 / send_log 落库。
package application

import (
	"context"
	"testing"

	hqv1 "github.com/hqpush/gate/packages/contract/go/hq/v1"
	"github.com/hqpush/gate/packages/obs"
	"github.com/hqpush/gate/services/notify/internal/channel"
)

// fakeDispatchStore 决策器持久化桩。
type fakeDispatchStore struct {
	vip       *VipRow
	email     string
	logs      []*SendLogRow
	logErr    error
	developer *DeveloperRow
}

func (f *fakeDispatchStore) VipByID(context.Context, int64) (*VipRow, error) {
	if f.vip == nil {
		return nil, nil
	}
	return f.vip, nil
}
func (f *fakeDispatchStore) EmailOfUser(context.Context, int64) (string, error) { return f.email, nil }
func (f *fakeDispatchStore) InsertSendLogs(_ context.Context, logs []*SendLogRow) error {
	f.logs = append(f.logs, logs...)
	return f.logErr
}
func (f *fakeDispatchStore) DeveloperByUser(context.Context, int64) (*DeveloperRow, error) {
	return f.developer, nil
}

// fakeChSender 记录发送的通道适配器桩。
type fakeChSender struct {
	name string
	n    int
}

func (f *fakeChSender) Name() string { return f.name }
func (f *fakeChSender) Send(context.Context, channel.OutboundMessage) (string, error) {
	f.n++
	return f.name + "-id", nil
}

func delivery() *hqv1.AlertDelivery {
	return &hqv1.AlertDelivery{
		EventId: "evt-1", UserId: 7, Market: hqv1.Market_A_SHARE,
		Symbol: "600000", Title: "测试告警", DetailJson: `{"changeRate":2.0}`, TriggerTs: 1788600000000,
	}
}

func newTestDispatcher(vip *VipRow, mail, sms, dd, feishu channel.Sender) (*Dispatcher, *fakeDispatchStore) {
	store := &fakeDispatchStore{vip: vip, email: "u@example.com"}
	d := NewDispatcher(store, mail, sms, dd, feishu,
		channel.NewCircuitBreaker(nil, channel.Limits{}),
		DispatchConfig{MajorPct: 9.8}, obs.NewLogger("test"), obs.NewMetrics())
	return d, store
}

func TestEmailDefaultOnlyMail(t *testing.T) {
	mail, sms := &fakeChSender{name: "mail"}, &fakeChSender{name: "sms"}
	d, store := newTestDispatcher(&VipRow{PlanType: "free", ChannelDefault: "email"}, mail, sms, nil, nil)
	d.Dispatch(context.Background(), delivery(), 0, "")
	if mail.n != 1 || sms.n != 0 {
		t.Fatalf("邮件默认：mail=%d sms=%d，期望 1/0", mail.n, sms.n)
	}
	if len(store.logs) != 1 || store.logs[0].Status != "SENT" {
		t.Fatalf("send_log 期望 1 条 SENT，got %+v", store.logs)
	}
}

func TestDingtalkBoundSendsIMPlusMail(t *testing.T) {
	dd, mail := &fakeChSender{name: "dd"}, &fakeChSender{name: "mail"}
	d, store := newTestDispatcher(&VipRow{
		PlanType: "vip1", ChannelDefault: "dd", DingtalkWebhook: "https://oapi.dingtalk.com/robot/send?access_token=x",
	}, mail, nil, dd, nil)
	d.Dispatch(context.Background(), delivery(), 0, "")
	if dd.n != 1 || mail.n != 1 {
		t.Fatalf("钉钉绑定：dd=%d mail=%d，期望 1/1（IM+邮件备份）", dd.n, mail.n)
	}
	if len(store.logs) != 2 {
		t.Fatalf("send_log 期望 2 条，got %d", len(store.logs))
	}
}

func TestDingtalkUnboundDegradesToMail(t *testing.T) {
	mail := &fakeChSender{name: "mail"}
	d, store := newTestDispatcher(&VipRow{PlanType: "vip1", ChannelDefault: "dd"}, mail, nil, nil, nil)
	d.Dispatch(context.Background(), delivery(), 0, "")
	if mail.n != 1 {
		t.Fatalf("未绑定降级：mail=%d 期望 1", mail.n)
	}
	for _, l := range store.logs {
		if l.Channel == "dd" {
			t.Fatal("未绑定时不应尝试钉钉通道")
		}
	}
}

func TestFreeUserSMSDenied(t *testing.T) {
	mail, sms := &fakeChSender{name: "mail"}, &fakeChSender{name: "sms"}
	// 免费用户 allow_sms=false，即使误配 channel_default=sms 也降级
	d, _ := newTestDispatcher(&VipRow{PlanType: "free", ChannelDefault: "sms"}, mail, sms, nil, nil)
	d.Dispatch(context.Background(), delivery(), 0, "")
	if sms.n != 0 {
		t.Fatalf("免费用户短信应被拒绝，实际发送 %d 条", sms.n)
	}
	if mail.n != 1 {
		t.Fatalf("降级邮件应发送 1 条，实际 %d", mail.n)
	}
}

func TestNoVipRowDefaultsToEmail(t *testing.T) {
	mail := &fakeChSender{name: "mail"}
	d, _ := newTestDispatcher(nil, mail, nil, nil, nil) // vip==nil → 免费口径
	d.Dispatch(context.Background(), delivery(), 0, "")
	if mail.n != 1 {
		t.Fatalf("无套餐默认邮件，mail=%d", mail.n)
	}
}

func TestSubscribePreferOverridesUserDefault(t *testing.T) {
	// 用户默认邮件，但订阅级 channel_prefer=dd（VIP 且已绑定）→ 走钉钉+邮件备份
	dd, mail := &fakeChSender{name: "dd"}, &fakeChSender{name: "mail"}
	d, _ := newTestDispatcher(&VipRow{
		PlanType: "vip1", ChannelDefault: "email", DingtalkWebhook: "https://oapi.dingtalk.com/robot?access_token=x",
	}, mail, nil, dd, nil)
	d.Dispatch(context.Background(), delivery(), 901, "dd")
	if dd.n != 1 || mail.n != 1 {
		t.Fatalf("订阅级覆盖：dd=%d mail=%d，期望 1/1", dd.n, mail.n)
	}
}

func TestSubscribePreferUnboundDegradesToMail(t *testing.T) {
	// 订阅级偏好 feishu 但未绑定飞书 → 降级邮件
	mail := &fakeChSender{name: "mail"}
	d, _ := newTestDispatcher(&VipRow{PlanType: "vip1", ChannelDefault: "email"}, mail, nil, nil, nil)
	d.Dispatch(context.Background(), delivery(), 901, "feishu")
	if mail.n != 1 {
		t.Fatalf("未绑定飞书应降级邮件，mail=%d", mail.n)
	}
}

func TestDeveloperWebhookSendLog(t *testing.T) {
	// webhook 通道经 d.send 落库（send_log channel=webhook）
	hook := &fakeChSender{name: "webhook"}
	store := &fakeDispatchStore{vip: &VipRow{PlanType: "free", ChannelDefault: "email"},
		email:     "dev@example.com",
		developer: &DeveloperRow{PushMode: "webhook", HookURL: "https://dev.example.com/hook", AppSecret: "s3cr3t"}}
	d := NewDispatcher(store, &fakeChSender{name: "mail"}, nil, nil, nil,
		channel.NewCircuitBreaker(nil, channel.Limits{}), DispatchConfig{}, obs.NewLogger("test"), obs.NewMetrics())
	d.send(context.Background(), "webhook", hook, channel.OutboundMessage{
		UserID: 7, EventID: "evt-1", RuleID: 42, Recipient: "https://dev.example.com/hook", Body: "{}"}, false)
	if hook.n != 1 {
		t.Fatalf("webhook send 应记录 1 次，got %d", hook.n)
	}
	if len(store.logs) != 1 || store.logs[0].Channel != "webhook" {
		t.Fatalf("webhook send_log 应落库: %+v", store.logs)
	}
}

func TestDeveloperWebhookInvalidURLSkipped(t *testing.T) {
	// 私网 hook_url 被 SSRF 校验拒绝 → 不触发 webhook 投递，用户邮件通道照常
	store := &fakeDispatchStore{vip: &VipRow{PlanType: "free", ChannelDefault: "email"}, email: "dev@example.com",
		developer: &DeveloperRow{PushMode: "webhook", HookURL: "http://127.0.0.1/hook", AppSecret: "s3cr3t"}}
	mail := &fakeChSender{name: "mail"}
	d := NewDispatcher(store, mail, nil, nil, nil,
		channel.NewCircuitBreaker(nil, channel.Limits{}), DispatchConfig{}, obs.NewLogger("test"), obs.NewMetrics())
	d.Dispatch(context.Background(), delivery(), 42, "")
	for _, l := range store.logs {
		if l.Channel == "webhook" {
			t.Fatal("私网 hook_url 不应触发 webhook 投递")
		}
	}
	if mail.n != 1 {
		t.Fatalf("邮件兜底应照常发送，mail=%d", mail.n)
	}
}
