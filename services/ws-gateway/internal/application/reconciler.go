// 套餐复核器（B22，ADR-040）：周期性地对在线连接按 user_id 复核 user_vip 权益，
// 降级/封号后存量连接在下一复核周期内被降权（差频降档 + 摘除越权 kline 周期）或保持。
// 查询走 Redis 缓存（biz-service 写路径同步 user_vip:{id}），避免逐连接打库。
package application

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/hqpush/gate/services/ws-gateway/internal/domain"
)

// UserVipReader 权益读取（repository.RedisUserVipStore 实现；接口定在消费方避免包环）。
type UserVipReader interface {
	// PlanType 返回用户套餐（free|vip1|vip2|vip3）；无缓存/格式非法返回 ("", false)。
	PlanType(ctx context.Context, uid int64) (string, bool)
}

// EntitlementReconciler 在线连接权益复核器。
type EntitlementReconciler struct {
	store    UserVipReader
	hub      *Hub
	interval time.Duration
	log      *slog.Logger
}

func NewEntitlementReconciler(store UserVipReader, hub *Hub, interval time.Duration, log *slog.Logger) *EntitlementReconciler {
	return &EntitlementReconciler{store: store, hub: hub, interval: interval, log: log}
}

// Run 启动复核循环（ctx 取消即停）。
func (r *EntitlementReconciler) Run(ctx context.Context) {
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.reconcile(ctx)
		}
	}
}

// reconcile 一次全量复核：遍历在线用户，按缓存套餐更新连接权益。
func (r *EntitlementReconciler) reconcile(ctx context.Context) {
	updated := 0
	for _, uid := range r.hub.UserIDs() {
		planType, ok := r.store.PlanType(ctx, uid)
		if !ok {
			continue // 无缓存（非 free 用户尚未写入/已失效）：保持现状，下周期再判
		}
		for _, c := range r.hub.UserConns(uid) {
			old := c.Ent.Load()
			if old != nil && old.PlanType == planType {
				continue
			}
			next := domain.NewEntitlement(planType)
			c.Ent.Store(next) // 整值替换，writeLoop 下轮按新权益差频
			if old != nil && old.IsVip() && !next.IsVip() {
				// 降级：摘除越权 kline 周期（free 仅 kline@1m）
				var drop []string
				for _, p := range c.KlineSubs.Periods() {
					if n, err := strconv.Atoi(p); err == nil && !next.KlineAllowed(int32(n)) {
						drop = append(drop, p)
					}
				}
				if len(drop) > 0 {
					c.KlineSubs.Remove(drop...)
				}
			}
			updated++
		}
	}
	if updated > 0 && r.log != nil {
		r.log.Info("entitlement reconciled", "conns_updated", updated, "interval", r.interval.String())
	}
}
