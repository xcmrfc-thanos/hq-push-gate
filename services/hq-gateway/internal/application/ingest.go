// Package application 用例编排；通过接口调用适配器，不直接依赖 Kafka。
package application

import (
	"context"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/hqpush/gate/packages/obs"
	"github.com/hqpush/gate/services/hq-gateway/internal/domain"
)

// TickPublisher 仓储接口（internal/repository 实现）。
type TickPublisher interface {
	PublishTick(ctx context.Context, t *domain.Tick) error
}

// IngestService 行情接入用例：标准化校验后发布到 tick_raw。
type IngestService struct {
	pub      TickPublisher
	log      *slog.Logger
	accepted *prometheus.CounterVec
	rejected *prometheus.CounterVec
}

func NewIngestService(pub TickPublisher, log *slog.Logger, m *obs.Metrics) *IngestService {
	return &IngestService{
		pub:      pub,
		log:      log,
		accepted: m.Counter("hq_gateway_ticks_accepted_total", "标准化通过的 tick 数"),
		rejected: m.Counter("hq_gateway_ticks_rejected_total", "标准化拒绝的 tick 数"),
	}
}

// Ingest 批量标准化并发布；返回被拒绝的数量。
func (s *IngestService) Ingest(ctx context.Context, ticks []*domain.Tick) (int, error) {
	rejected := 0
	for _, t := range ticks {
		if err := t.Validate(); err != nil {
			rejected++
			s.rejected.WithLabelValues().Inc()
			continue
		}
		if err := s.pub.PublishTick(ctx, t); err != nil {
			return rejected, err
		}
		s.accepted.WithLabelValues().Inc()
	}
	if rejected > 0 {
		s.log.Warn("ingest rejected ticks", "count", rejected, "total", len(ticks))
	}
	return rejected, nil
}
