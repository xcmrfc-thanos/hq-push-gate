// 成本熔断计数器（docs/10 §4.3）：全局/单用户日额度，Redis rl: 键位（docs/04 §5 预留）。
// INCR + 当日 24 点过期；Allow 返回 false 即触发降级（邮件永远兜底）。
package channel

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type Limits struct {
	MailDaily     int64 // 全局邮件日额度，<=0 不限
	SmsDaily      int64 // 全局短信日额度，<=0 不限
	SmsUserDaily  int64 // 单用户短信日上限，<=0 不限
	MailUserDaily int64 // 单用户邮件日上限，<=0 不限
}

type CircuitBreaker struct {
	rdb    *redis.Client
	limits Limits
}

func NewCircuitBreaker(rdb *redis.Client, limits Limits) *CircuitBreaker {
	return &CircuitBreaker{rdb: rdb, limits: limits}
}

// dayKey 当日额度键（按 CST 日界；业务"日额度"以北京时间为准，非 UTC）。
func dayKey(scope string) string {
	return fmt.Sprintf("rl:%s:daily:%s", scope, cstNow().Format("20060102"))
}

// cstNow 当前北京时间。
func cstNow() time.Time { return time.Now().In(cstLoc) }

// cstEOD 距下一个 CST 午夜的时长（额度键 TTL）。
func cstEOD() time.Duration {
	n := cstNow()
	next := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, cstLoc).Add(24 * time.Hour)
	return next.Sub(time.Now())
}

var cstLoc = time.FixedZone("CST", 8*3600)

// Allow 判断渠道当前是否可用（只读检查，不计数；计数在 Confirm 成功发送后）。
func (b *CircuitBreaker) Allow(ctx context.Context, ch string, userID int64) bool {
	if b == nil || b.rdb == nil {
		return true // 无 Redis 降级为不限（保告警优先）
	}
	if b.limits.MailDaily > 0 && ch == "mail" {
		if n, err := b.rdb.Get(ctx, dayKey("mail")).Int64(); err == nil && n >= b.limits.MailDaily {
			return false
		}
	}
	if b.limits.SmsDaily > 0 && ch == "sms" {
		if n, err := b.rdb.Get(ctx, dayKey("sms")).Int64(); err == nil && n >= b.limits.SmsDaily {
			return false
		}
	}
	if userID > 0 {
		switch ch {
		case "mail":
			if b.limits.MailUserDaily > 0 {
				if n, err := b.rdb.Get(ctx, fmt.Sprintf("rl:mail:user:%d:%s", userID, cstNow().Format("20060102"))).Int64(); err == nil && n >= b.limits.MailUserDaily {
					return false
				}
			}
		case "sms":
			if b.limits.SmsUserDaily > 0 {
				if n, err := b.rdb.Get(ctx, fmt.Sprintf("rl:sms:user:%d:%s", userID, cstNow().Format("20060102"))).Int64(); err == nil && n >= b.limits.SmsUserDaily {
					return false
				}
			}
		}
	}
	return true
}

// Confirm 成功发送后计数（INCR 并对当日键设置过期）。
func (b *CircuitBreaker) Confirm(ctx context.Context, ch string, userID int64) {
	if b == nil || b.rdb == nil {
		return
	}
	eod := cstEOD()
	if eod <= 0 {
		eod = 24 * time.Hour
	}
	b.rdb.Incr(ctx, dayKey(ch))
	b.rdb.Expire(ctx, dayKey(ch), eod)
	if userID > 0 {
		uk := fmt.Sprintf("rl:%s:user:%d:%s", ch, userID, cstNow().Format("20060102"))
		b.rdb.Incr(ctx, uk)
		b.rdb.Expire(ctx, uk, eod)
	}
}
