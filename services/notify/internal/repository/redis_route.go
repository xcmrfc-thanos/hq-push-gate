package repository

import (
	"context"
	"strconv"

	"github.com/redis/go-redis/v9"

	"github.com/hqpush/gate/packages/contract"
)

// RedisRoute 告警投递路由索引：
// conn:{userId} -> gatewayId（在线实例）；
// gwidslots:{gatewayId} -> 该实例租约持有的槽位集合。
type RedisRoute struct{ r *redis.Client }

func NewRedisRoute(r *redis.Client) *RedisRoute { return &RedisRoute{r: r} }

// GatewayOfUser 用户当前连接所在实例；空串表示不在线。
func (s *RedisRoute) GatewayOfUser(ctx context.Context, userID int64) (string, error) {
	v, err := s.r.Get(ctx, contract.RedisConnPrefix+strconv.FormatInt(userID, 10)).Result()
	if err == redis.Nil {
		return "", nil
	}
	return v, err
}

// SlotsOfGateway 实例持有的槽位（无槽位视为不可路由）。
func (s *RedisRoute) SlotsOfGateway(ctx context.Context, gatewayID string) ([]uint32, error) {
	vals, err := s.r.SMembers(ctx, contract.RedisGwSlotsPfx+gatewayID).Result()
	if err != nil {
		return nil, err
	}
	out := make([]uint32, 0, len(vals))
	for _, v := range vals {
		if n, err := strconv.ParseUint(v, 10, 32); err == nil {
			out = append(out, uint32(n))
		}
	}
	return out, nil
}
