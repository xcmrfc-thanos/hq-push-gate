// Redis 票据存取：open:ws:{ticket} → uid（biz /open/v1/ws-ticket 签发，60s TTL）。
package repository

import (
	"context"
	"strconv"

	"github.com/redis/go-redis/v9"
)

// RedisTicketStore TicketStore 的 Redis 实现（取后即焚，一次性票据）。
type RedisTicketStore struct{ rdb *redis.Client }

func NewRedisTicketStore(rdb *redis.Client) *RedisTicketStore { return &RedisTicketStore{rdb: rdb} }

func (s *RedisTicketStore) Take(ctx context.Context, ticket string) (int64, bool) {
	v, err := s.rdb.Get(ctx, "open:ws:"+ticket).Result()
	if err != nil || v == "" {
		return 0, false
	}
	s.rdb.Del(ctx, "open:ws:"+ticket) // 一次性：无论解析成败都焚毁
	uid, err := strconv.ParseInt(v, 10, 64)
	if err != nil || uid <= 0 {
		return 0, false
	}
	return uid, true
}
