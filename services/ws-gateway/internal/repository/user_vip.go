// Redis user_vip 权益缓存读（B22，ADR-040：复核走 Redis 免打库）。
// 键 `user_vip:{id}` 由 biz-service 写路径维护（grant/expireOverdue 同步，TTL 5min）；
// 值格式 `planType|expireEpochSec`（expire 为空或 0 = 无期限；expire 已过按 free）。
package repository

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// UserVipPrefix user_vip 缓存键前缀（与 biz-service VipCache 一致）。
const UserVipPrefix = "user_vip:"

// UserVipStore 权益读取（复核器/建连用）。
type UserVipStore interface {
	// PlanType 返回用户套餐（free|vip1|vip2|vip3）；无缓存/格式非法返回 ("", false)。
	PlanType(ctx context.Context, uid int64) (string, bool)
}

// RedisUserVipStore UserVipStore 的 Redis 实现。
type RedisUserVipStore struct{ rdb *redis.Client }

func NewRedisUserVipStore(rdb *redis.Client) *RedisUserVipStore { return &RedisUserVipStore{rdb: rdb} }

func (s *RedisUserVipStore) PlanType(ctx context.Context, uid int64) (string, bool) {
	v, err := s.rdb.Get(ctx, UserVipPrefix+strconv.FormatInt(uid, 10)).Result()
	if err != nil || v == "" {
		return "", false
	}
	planType, expireSec, ok := ParseUserVipValue(v)
	if !ok {
		return "", false
	}
	// 缓存值中的到期时间已过 → 按 free（与 biz-service VipCache 一致；到期降级 job 前的窗口期兜底）
	if expireSec > 0 && time.Unix(expireSec, 0).Before(time.Now()) {
		return "free", true
	}
	return planType, true
}

// ParseUserVipValue 解析 `planType|expireEpochSec`（expire 段可为空）。
func ParseUserVipValue(v string) (planType string, expireSec int64, ok bool) {
	bar := strings.IndexByte(v, '|')
	pt := v
	if bar >= 0 {
		pt = v[:bar]
		exp := v[bar+1:]
		if exp != "" {
			n, err := strconv.ParseInt(exp, 10, 64)
			if err != nil {
				return "", 0, false
			}
			expireSec = n
		}
	}
	if pt == "" {
		return "", 0, false
	}
	return pt, expireSec, true
}
