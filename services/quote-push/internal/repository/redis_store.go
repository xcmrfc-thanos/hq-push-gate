// Package repository 基础设施适配器：Redis 路由索引/快照写入。
package repository

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/hqpush/gate/packages/contract"
	hqv1 "github.com/hqpush/gate/packages/contract/go/hq/v1"
	"github.com/hqpush/gate/services/quote-push/internal/domain"
)

// RedisStore 路由索引与快照访问。
type RedisStore struct{ r *redis.Client }

func NewRedisStore(r *redis.Client) *RedisStore { return &RedisStore{r: r} }

// SymbolGateways 返回订阅某 symbol 的 gateway 实例集合（symgw:{market}:{symbol}）。
func (s *RedisStore) SymbolGateways(ctx context.Context, symKey string) ([]string, error) {
	return s.r.SMembers(ctx, contract.RedisSymGwPrefix+symKey).Result()
}

// GatewaySlots 返回实例租约持有的槽位（gwidslots:{gatewayId}）。
func (s *RedisStore) GatewaySlots(ctx context.Context, gatewayID string) ([]uint32, error) {
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

// WriteSnapshots 批量写热快照 snap:{market}:{symbol}（TTL 1d，docs/04 §5）。
// 涨跌幅 pct 由 last/pre_close 计算。
func (s *RedisStore) WriteSnapshots(ctx context.Context, ticks []*hqv1.Tick) error {
	pipe := s.r.Pipeline()
	for _, t := range ticks {
		key := contract.RedisSnapPrefix + t.Market.String() + ":" + t.Symbol
		pipe.HSet(ctx, key, map[string]any{
			"last":      t.LastPrice,
			"pct":       domain.Pct(t.LastPrice, t.PreClose),
			"vol":       t.Volume,
			"amount":    t.Amount,
			"pre_close": t.PreClose,
			"ts":        t.TimestampMs,
		})
		pipe.Expire(ctx, key, 24*time.Hour)
	}
	_, err := pipe.Exec(ctx)
	return err
}

// WriteKlineCur 写当前闭合 K 线 kline:cur:{market}:{symbol}:{period}
// （订阅即回补的"当前值"；值 = 原始 proto 字节，TTL 1d。docs/12 B21）。
func (s *RedisStore) WriteKlineCur(ctx context.Context, market, symbol string, periodMin int, raw []byte) error {
	key := contract.RedisKlineCurPfx + market + ":" + symbol + ":" + strconv.Itoa(periodMin)
	return s.r.Set(ctx, key, raw, 24*time.Hour).Err()
}
