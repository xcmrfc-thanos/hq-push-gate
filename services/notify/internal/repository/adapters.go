package repository

import (
	"context"
	"strconv"

	hqv1 "github.com/hqpush/gate/packages/contract/go/hq/v1"

	"github.com/hqpush/gate/packages/contract"
	"github.com/hqpush/gate/packages/kafkax"
	"github.com/hqpush/gate/services/notify/internal/application"
	"github.com/hqpush/gate/services/notify/internal/domain"
)

func formatSlot(slot uint32) string { return strconv.FormatUint(uint64(slot), 10) }

// alertStore 适配 MySQLStore -> application.Store。
type alertStore struct{ m *MySQLStore }

func NewAlertStore(m *MySQLStore) application.Store { return &alertStore{m: m} }

func (a *alertStore) RuleByID(ctx context.Context, id int64) (*application.RuleRow, error) {
	r, err := a.m.RuleByID(ctx, id)
	if err != nil || r == nil {
		return nil, err
	}
	return &application.RuleRow{
		ID: r.ID, UserID: r.UserID, Market: r.Market, Symbol: r.Symbol,
		ChannelPrefer: r.ChannelPrefer, // 订阅级渠道偏好（docs/10 §3.3），丢失会导致降级决策失效
	}, nil
}

func (a *alertStore) InsertAlertRecords(ctx context.Context, records []*domain.AlertRecord) (int64, error) {
	return a.m.InsertAlertRecords(ctx, records)
}

func (a *alertStore) MarkSent(ctx context.Context, ids []string) error {
	return a.m.MarkSent(ctx, ids)
}

// kafkaPublisher 适配 kafkax.Writer -> application.Publisher（ws_push 槽位 key）。
type kafkaPublisher struct{ w *kafkax.Writer }

func NewKafkaPublisher(w *kafkax.Writer) application.Publisher { return &kafkaPublisher{w: w} }

// ruleBcastPublisher 适配 kafkax.Writer -> transport.RuleBcastPublisher（退订撤销规则）。
type ruleBcastPublisher struct{ w *kafkax.Writer }

func NewRuleBcastPublisher(w *kafkax.Writer) *ruleBcastPublisher { return &ruleBcastPublisher{w: w} }

func (p *ruleBcastPublisher) PublishRuleDelete(ctx context.Context, msg *hqv1.RuleMsg) error {
	return p.w.PublishProto(ctx, contract.TopicRuleBcast, strconv.FormatInt(msg.RuleId, 10), msg)
}

func (p *kafkaPublisher) PublishAlert(ctx context.Context, gatewayID string, slot uint32, d *hqv1.AlertDelivery) error {
	msg := &hqv1.WsPushMsg{
		GatewayId:   gatewayID,
		GatewaySlot: slot,
		Payload:     &hqv1.WsPushMsg_Alert{Alert: d},
	}
	return p.w.PublishProto(ctx, contract.TopicWsPush, formatSlot(slot), msg)
}
