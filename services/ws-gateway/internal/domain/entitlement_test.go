package domain

import (
	"testing"
	"time"
)

func TestEntitlementQuoteInterval(t *testing.T) {
	cases := []struct {
		plan string
		want time.Duration
	}{
		{"", QuoteIntervalFree},
		{"free", QuoteIntervalFree},
		{"vip1", QuoteIntervalVip},
		{"vip2", QuoteIntervalVip},
		{"vip3", QuoteIntervalVip},
		{"unknown", QuoteIntervalFree},
	}
	for _, c := range cases {
		got := NewEntitlement(c.plan).QuoteInterval
		if got != c.want {
			t.Errorf("plan=%q QuoteInterval=%v want %v", c.plan, got, c.want)
		}
	}
}

func TestEntitlementKlineAllowed(t *testing.T) {
	free := NewEntitlement("free")
	vip := NewEntitlement("vip1")
	if !free.KlineAllowed(1) {
		t.Error("free 应允许 kline@1m")
	}
	if free.KlineAllowed(3) || free.KlineAllowed(60) || free.KlineAllowed(1440) {
		t.Error("free 不应允许 kline@3m+")
	}
	for _, p := range []int32{1, 3, 5, 15, 30, 60, 1440} {
		if !vip.KlineAllowed(p) {
			t.Errorf("vip 应允许 kline@%dm", p)
		}
	}
}
