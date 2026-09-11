package application

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/hqpush/gate/packages/contract"
)

// SubIndex 订阅聚合索引（Redis 适配）：
// symgw:{market}:{symbol} <- gatewayId 集合（quote-push 路由用）
// gwsubs:{gatewayId}      <- market:symbol 集合（实例订阅摘要）
// conn:{userId}           <- gatewayId（TTL 心跳×3）
type SubIndex struct {
	r        *redis.Client
	gatewayID string
	ttl      time.Duration
}

func NewSubIndex(r *redis.Client, gatewayID string, ttl time.Duration) *SubIndex {
	return &SubIndex{r: r, gatewayID: gatewayID, ttl: ttl}
}

// Subscribe 登记本实例对该 symbol 的需要（幂等）。
func (s *SubIndex) Subscribe(ctx context.Context, symKeys ...string) error {
	pipe := s.r.Pipeline()
	for _, k := range symKeys {
		pipe.SAdd(ctx, contract.RedisSymGwPrefix+k, s.gatewayID)
		pipe.Expire(ctx, contract.RedisSymGwPrefix+k, s.ttl)
		pipe.SAdd(ctx, contract.RedisGwSubsPrefix+s.gatewayID, k)
	}
	pipe.Expire(ctx, contract.RedisGwSubsPrefix+s.gatewayID, s.ttl)
	_, err := pipe.Exec(ctx)
	return err
}

// Unsubscribe 移除本实例对该 symbol 的需要；
// symgw 集合只有在本实例不再需要时才移除实例成员。
func (s *SubIndex) Unsubscribe(ctx context.Context, symKeys ...string) error {
	pipe := s.r.Pipeline()
	for _, k := range symKeys {
		pipe.SRem(ctx, contract.RedisGwSubsPrefix+s.gatewayID, k)
		pipe.SRem(ctx, contract.RedisSymGwPrefix+k, s.gatewayID)
	}
	_, err := pipe.Exec(ctx)
	return err
}

// Touch 心跳续期：conn:{userId} 与订阅摘要 TTL。
func (s *SubIndex) Touch(ctx context.Context, userIDs []int64, symKeys []string) error {
	pipe := s.r.Pipeline()
	for _, uid := range userIDs {
		pipe.Set(ctx, contract.RedisConnPrefix+strconv.FormatInt(uid, 10), s.gatewayID, s.ttl)
	}
	if len(symKeys) > 0 {
		pipe.Expire(ctx, contract.RedisGwSubsPrefix+s.gatewayID, s.ttl)
		for _, k := range symKeys {
			pipe.Expire(ctx, contract.RedisSymGwPrefix+k, s.ttl)
		}
	}
	_, err := pipe.Exec(ctx)
	return err
}
