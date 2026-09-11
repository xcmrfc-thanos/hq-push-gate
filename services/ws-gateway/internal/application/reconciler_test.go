package application

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/hqpush/gate/services/ws-gateway/internal/domain"
)

// fakeVip fake UserVipReader。
type fakeVip struct{ plan map[int64]string }

func (f *fakeVip) PlanType(_ context.Context, uid int64) (string, bool) {
	p, ok := f.plan[uid]
	return p, ok
}

// newTestHub 构造带 gauge 的 Hub（TotalGauge.Add 会 Set，须真实 gauge）。
func newTestHub() *Hub {
	return NewHub(NewTotalGauge(prometheus.NewGauge(prometheus.GaugeOpts{Name: "test_total"})))
}

// newTestConn 构造带权益与 kline 订阅的连接。
func newTestConn(uid int64, planType string, klinePeriods ...string) *Conn {
	c := &Conn{UserID: uid, KlineSubs: domain.NewKlinePeriods()}
	c.Ent.Store(domain.NewEntitlement(planType))
	if len(klinePeriods) > 0 {
		c.KlineSubs.Add(klinePeriods...)
	}
	return c
}

func TestReconcilerDowngradeDropsOvertierKline(t *testing.T) {
	hub := newTestHub()
	vip := newTestConn(1, "vip1", "1", "5", "60")
	free := newTestConn(2, "free", "1")
	hub.Register(vip)
	hub.Register(free)

	r := NewEntitlementReconciler(&fakeVip{plan: map[int64]string{1: "free"}}, hub, time.Minute, nil)
	r.reconcile(context.Background())

	// 用户 1 从 vip1 降为 free：权益降档 + 摘除 kline@5m/60m，保留 1m
	if got := vip.Ent.Load().PlanType; got != "free" {
		t.Errorf("uid1 应降为 free，got %q", got)
	}
	if vip.Ent.Load().QuoteInterval != domain.QuoteIntervalFree {
		t.Errorf("uid1 差频应降为 free 档")
	}
	if vip.KlineSubs.Has("1") == false {
		t.Error("uid1 应保留 kline@1m")
	}
	if vip.KlineSubs.Has("5") || vip.KlineSubs.Has("60") {
		t.Error("uid1 降级后应摘除 kline@5m/60m")
	}

	// 用户 2 保持 free：不动
	if free.Ent.Load().PlanType != "free" {
		t.Errorf("uid2 不应被改动")
	}
}

func TestReconcilerUpgradeAndNoChange(t *testing.T) {
	hub := newTestHub()
	free := newTestConn(1, "free")
	stable := newTestConn(2, "vip2", "60")
	hub.Register(free)
	hub.Register(stable)

	r := NewEntitlementReconciler(&fakeVip{plan: map[int64]string{1: "vip3"}}, hub, time.Minute, nil)
	r.reconcile(context.Background())

	if free.Ent.Load().PlanType != "vip3" || free.Ent.Load().QuoteInterval != domain.QuoteIntervalVip {
		t.Error("free→vip3 升级应生效（差频升档）")
	}
	if stable.Ent.Load().PlanType != "vip2" {
		t.Error("套餐未变不应重写权益")
	}
}
