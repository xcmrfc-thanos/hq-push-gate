package application

import (
	"context"
	"time"

	"github.com/hqpush/gate/services/notify/internal/domain"
)

// PullService 告警补拉与 ACK 用例（notify/transport 调用）。
type PullService struct {
	store  PullStore
	window time.Duration
}

// PullStore application 侧需要的持久化接口。
type PullStore interface {
	PullPending(ctx context.Context, userID, cursor int64, limit int, now time.Time, window time.Duration) ([]*domain.AlertRecord, error)
	Ack(ctx context.Context, deliveryID string) (bool, error)
}

func NewPullService(store PullStore, window time.Duration) *PullService {
	return &PullService{store: store, window: window}
}

// Pull 返回补拉窗口内未 ACK 未过期的投递（按 delivery_id 幂等，客户端去重）。
func (s *PullService) Pull(ctx context.Context, userID, cursor int64, limit int) ([]*domain.AlertRecord, error) {
	return s.store.PullPending(ctx, userID, cursor, limit, time.Now(), s.window)
}

// Ack 幂等 ACK。
func (s *PullService) Ack(ctx context.Context, deliveryID string) (bool, error) {
	return s.store.Ack(ctx, deliveryID)
}
