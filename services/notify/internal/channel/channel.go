// Package channel 通知渠道适配器（docs/10 §4.2）：统一 Send 接口，实现挂具体服务商。
// 熔断/额度/降级决策不在本包——由 application.Dispatcher 在调用前完成。
package channel

import (
	"context"
	"errors"
)

// OutboundMessage 出站消息（渠道无关的规范化载荷）。
type OutboundMessage struct {
	UserID    int64  // 接收用户
	EventID   string // 关联告警事件
	RuleID    int64  // 关联规则（0=系统级，如退订确认）
	Subject   string // 邮件主题
	Body      string // 正文（纯文本；邮件适配器附加风险提示与退订说明）
	Recipient string // 收件地址（邮箱/手机号/hook_url；空则由适配器按 UserID 回查）
}

// ErrChannelUnavailable 渠道不可用（未配置/上游异常）——Dispatcher 据此降级。
var ErrChannelUnavailable = errors.New("channel unavailable")

// Sender 渠道统一发送接口；providerMsgID 供回执对账（notify_send_log）。
type Sender interface {
	Send(ctx context.Context, msg OutboundMessage) (providerMsgID string, err error)
	Name() string // mail|sms|webhook
}
