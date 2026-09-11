// Package application 用例编排：alert-inbox 幂等落库、用户展开、ws_push 投递、重试。
package application

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	hqv1 "github.com/hqpush/gate/packages/contract/go/hq/v1"

	"github.com/hqpush/gate/packages/contract"
	"github.com/hqpush/gate/packages/obs"
	"github.com/hqpush/gate/services/notify/internal/domain"
)

// Store alert-inbox 持久化接口（MySQL 适配实现）。
type Store interface {
	RuleByID(ctx context.Context, id int64) (*RuleRow, error)
	InsertAlertRecords(ctx context.Context, records []*domain.AlertRecord) (int64, error)
	MarkSent(ctx context.Context, deliveryIDs []string) error
}

// RuleRow 规则查询结果（避免 repository 类型泄漏进 application 的循环依赖）。
type RuleRow struct {
	ID            int64
	UserID        int64
	Market        string
	Symbol        string
	ChannelPrefer string // sms|email|""（""=继承 user_vip.channel_default，docs/10 §3.3）
}

// RouteIndex 投递路由接口（Redis 适配实现）。
type RouteIndex interface {
	GatewayOfUser(ctx context.Context, userID int64) (string, error)
	SlotsOfGateway(ctx context.Context, gatewayID string) ([]uint32, error)
}

// Publisher ws_push 生产接口（kafkax 适配实现，槽位 key）。
type Publisher interface {
	PublishAlert(ctx context.Context, gatewayID string, slot uint32, d *hqv1.AlertDelivery) error
}

// InboxService alert-inbox 用例：落库 → 展开 → 路由投递 → 状态推进。
type InboxService struct {
	store     Store
	route     RouteIndex
	pub       Publisher
	log       *slog.Logger
	window    time.Duration
	delivered *prometheus.CounterVec
	offline   *prometheus.CounterVec
	onDeliver func(ctx context.Context, d *hqv1.AlertDelivery, ruleID int64, prefer string) // 渠道通知钩子（Dispatcher，prefer=订阅级偏好）
}

func NewInboxService(store Store, route RouteIndex, pub Publisher, log *slog.Logger, m *obs.Metrics, window time.Duration) *InboxService {
	return &InboxService{
		store: store, route: route, pub: pub, log: log, window: window,
		delivered: m.Counter("notify_alert_delivered_total", "成功路由到 ws_push 的投递数", "gateway"),
		offline:   m.Counter("notify_alert_offline_total", "用户离线、仅落库等待补拉的投递数"),
	}
}

// SetDeliverHook 注册渠道通知钩子（main 装配 Dispatcher.Dispatch；落库后触发，
// ruleID=关联规则，prefer=订阅级渠道偏好 alert_rule.channel_prefer）。
func (s *InboxService) SetDeliverHook(fn func(ctx context.Context, d *hqv1.AlertDelivery, ruleID int64, prefer string)) {
	s.onDeliver = fn
}

// HandleAlertEvent 消费 alert_event：
//  1. 幂等落库（PENDING），重复 event/user 组合由 delivery_id 唯一键去重；
//  2. 按规则展开用户（RuleMsg 冻结约束：不携带 user_ids）；
//  3. 在线用户路由到其所在 ws-gateway 槽位，投递成功推进 SENT；
//  4. 离线用户保持 PENDING，等待 72h 窗口内 REST 补拉。
func (s *InboxService) HandleAlertEvent(ctx context.Context, evt *hqv1.AlertEvent) error {
	rule, err := s.store.RuleByID(ctx, evt.RuleId)
	if err != nil {
		return fmt.Errorf("query rule %d: %w", evt.RuleId, err)
	}
	if rule == nil {
		// 规则已删除：事件无投递对象，记录后直接提交（不进死信）
		s.log.Warn("alert event for missing rule", "rule_id", evt.RuleId, "event_id", evt.EventId)
		return nil
	}
	triggerAt := time.UnixMilli(evt.TriggerTs)
	records := []*domain.AlertRecord{{
		DeliveryID: contract.DeliveryID(evt.EventId, rule.UserID),
		EventID:    evt.EventId,
		RuleID:     evt.RuleId,
		UserID:     rule.UserID,
		Market:     evt.Market.String(),
		Symbol:     evt.Symbol,
		Title:      evt.Title,
		TriggerAt:  triggerAt,
		Status:     domain.StatusPending,
	}}
	if _, err := s.store.InsertAlertRecords(ctx, records); err != nil {
		return fmt.Errorf("inbox insert: %w", err)
	}

	delivery := &hqv1.AlertDelivery{
		DeliveryId: records[0].DeliveryID,
		EventId:    evt.EventId,
		UserId:     rule.UserID,
		Market:     evt.Market,
		Symbol:     evt.Symbol,
		Title:      evt.Title,
		DetailJson: evt.DetailJson,
		TriggerTs:  evt.TriggerTs,
	}
	// 渠道通知（邮件/IM/短信）在落库后无条件触发——独立于 WS 在线状态：
	// 离线用户正是依赖邮件/IM 兜底的群体（docs/10 §4"邮件永远兜底"）
	if s.onDeliver != nil {
		s.onDeliver(ctx, delivery, evt.RuleId, rule.ChannelPrefer)
	}
	delivered, err := s.deliverOne(ctx, delivery)
	if err != nil {
		return err
	}
	if delivered {
		return s.store.MarkSent(ctx, []string{delivery.DeliveryId})
	}
	return nil // 保持 PENDING，等待 72h 窗口内补拉
}

// deliverOne 投递单用户；离线/无槽位不算错误，返回 false（保持 PENDING 可补拉）。
func (s *InboxService) deliverOne(ctx context.Context, d *hqv1.AlertDelivery) (bool, error) {
	gw, err := s.route.GatewayOfUser(ctx, d.UserId)
	if err != nil {
		return false, fmt.Errorf("route lookup user %d: %w", d.UserId, err)
	}
	if gw == "" {
		s.offline.WithLabelValues().Inc()
		return false, nil
	}
	slots, err := s.route.SlotsOfGateway(ctx, gw)
	if err != nil {
		return false, fmt.Errorf("route slots %s: %w", gw, err)
	}
	if len(slots) == 0 {
		s.offline.WithLabelValues().Inc()
		return false, nil
	}
	if err := s.pub.PublishAlert(ctx, gw, slots[0], d); err != nil {
		return false, fmt.Errorf("publish ws_push: %w", err)
	}
	s.delivered.WithLabelValues(gw).Inc()
	return true, nil
}
