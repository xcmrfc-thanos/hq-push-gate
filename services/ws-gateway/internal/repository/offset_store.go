package repository

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/hqpush/gate/packages/contract"
	"github.com/hqpush/gate/services/ws-gateway/internal/application"
)

// OffsetStore ws_push 分区 offset 的 Redis 存取（key: offset:ws_push:{slot}）。
type OffsetStore struct{ r *redis.Client }

func NewOffsetStore(r *redis.Client) *OffsetStore { return &OffsetStore{r: r} }

func (s *OffsetStore) GetSlotOffset(ctx context.Context, slot uint32) (int64, bool, error) {
	v, err := s.r.Get(ctx, contract.RedisWsPushOffset+":"+strconv.FormatUint(uint64(slot), 10)).Result()
	if err == redis.Nil {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	off, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, false, nil
	}
	return off, true, nil
}

func (s *OffsetStore) SetSlotOffset(ctx context.Context, slot uint32, offset int64) error {
	// ws_push 保留 1h，offset 记录保留 2h 即可
	return s.r.Set(ctx, contract.RedisWsPushOffset+":"+strconv.FormatUint(uint64(slot), 10),
		offset, 2*time.Hour).Err()
}

var _ application.RedisOffsetStore = (*OffsetStore)(nil)
