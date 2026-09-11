// Package repository 基础设施适配器：Kafka 生产（tick_raw）。
package repository

import (
	"context"

	"github.com/hqpush/gate/packages/contract"
	hqv1 "github.com/hqpush/gate/packages/contract/go/hq/v1"
	"github.com/hqpush/gate/packages/kafkax"
	"github.com/hqpush/gate/services/hq-gateway/internal/domain"
)

// KafkaTickPublisher 将领域 Tick 转为 protobuf 契约消息并写入 tick_raw（key=market:symbol）。
type KafkaTickPublisher struct{ w *kafkax.Writer }

func NewKafkaTickPublisher(w *kafkax.Writer) *KafkaTickPublisher { return &KafkaTickPublisher{w: w} }

func (p *KafkaTickPublisher) PublishTick(ctx context.Context, t *domain.Tick) error {
	return p.w.PublishProto(ctx, contract.TopicTickRaw, t.Key(), &hqv1.Tick{
		Market:      hqv1.Market(t.Market),
		Symbol:      t.Symbol,
		Exchange:    t.Exchange,
		TimestampMs: t.TimestampMS,
		LastPrice:   t.LastPrice,
		Open:        t.Open,
		High:        t.High,
		Low:         t.Low,
		PreClose:    t.PreClose,
		Volume:      t.Volume,
		Amount:      t.Amount,
	})
}
