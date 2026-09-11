package application

import (
	"testing"

	"github.com/hqpush/gate/packages/contract/wsproto"
	"github.com/hqpush/gate/services/ws-gateway/internal/domain"
)

// newDispatchConn 反向索引分发测试用的完整连接（复用 reconciler_test 的构造器补齐通道）。
func newDispatchConn(uid int64) *Conn {
	c := newTestConn(uid, domain.PlanFree)
	c.Subs = domain.NewSubs(200)
	c.Quotes = domain.NewQuoteBuffer()
	c.KlineCh = make(chan wsproto.KlinePush, 4)
	c.AlertCh = make(chan wsproto.AlertPush, 16)
	c.SysCh = make(chan wsproto.SysData, 8)
	c.Done = make(chan struct{})
	return c
}

// link 模拟 serveConn 的订阅路径：Subs 接纳 + 反向索引登记（键与 Subs 精确一致）。
func link(h *Hub, c *Conn, symKeys ...string) {
	var added []string
	for _, k := range symKeys {
		if c.Subs.Has(k) {
			continue
		}
		if !c.Subs.Add(k) {
			break
		}
		added = append(added, k)
	}
	if len(added) > 0 {
		h.LinkSym(c, added...)
	}
}

func symSet(h *Hub) map[string]bool {
	out := make(map[string]bool)
	for _, k := range h.Symbols() {
		out[k] = true
	}
	return out
}

func TestDispatchQuoteHitsIndexedOnly(t *testing.T) {
	h := newTestHub()
	a, b := newDispatchConn(1), newDispatchConn(2)
	h.Register(a)
	h.Register(b)
	link(h, a, "m:600000")
	link(h, b, "m:000001")

	if n := h.DispatchQuote("m:600000", wsproto.QuotePush{Symbol: "m:600000", Last: 10}); n != 1 {
		t.Fatalf("expect 1 hit, got %d", n)
	}
	if got := a.Quotes.Drain(); len(got) != 1 || got[0].Symbol != "m:600000" {
		t.Fatalf("expect a buffered 600000, got %+v", got)
	}
	if got := b.Quotes.Drain(); len(got) != 0 {
		t.Fatalf("expect b untouched, got %+v", got)
	}
}

func TestUnlinkRemovesDispatchTarget(t *testing.T) {
	h := newTestHub()
	a := newDispatchConn(1)
	h.Register(a)
	link(h, a, "m:600000")
	a.Subs.Remove("m:600000")
	h.UnlinkSym(a, "m:600000")
	if n := h.DispatchQuote("m:600000", wsproto.QuotePush{Symbol: "m:600000"}); n != 0 {
		t.Fatalf("expect 0 after unlink, got %d", n)
	}
	if _, ok := symSet(h)["m:600000"]; ok {
		t.Fatal("expect symbol removed from index keys")
	}
	h.UnlinkSym(a, "m:600000") // 幂等：二次摘除不 panic
}

func TestUnregisterCleansIndex(t *testing.T) {
	h := newTestHub()
	a := newDispatchConn(1)
	h.Register(a)
	link(h, a, "m:300750")
	h.Unregister(a)
	if n := h.DispatchQuote("m:300750", wsproto.QuotePush{Symbol: "m:300750"}); n != 0 {
		t.Fatalf("expect 0 after unregister, got %d", n)
	}
	if syms := h.Symbols(); len(syms) != 0 {
		t.Fatalf("expect empty symbols, got %v", syms)
	}
}

func TestDispatchKlineRequiresSymbolAndPeriod(t *testing.T) {
	h := newTestHub()
	a := newDispatchConn(1)
	h.Register(a)
	link(h, a, "m:600000")
	a.KlineSubs.Add("1")

	if n := h.DispatchKline("m:600000", 1, wsproto.KlinePush{Symbol: "m:600000", Period: 1}); n != 1 {
		t.Fatalf("expect 1 kline hit, got %d", n)
	}
	if n := h.DispatchKline("m:600000", 5, wsproto.KlinePush{Symbol: "m:600000", Period: 5}); n != 0 {
		t.Fatalf("expect 0 for unsubscribed period, got %d", n)
	}
	if n := h.DispatchKline("m:000001", 1, wsproto.KlinePush{Symbol: "m:000001", Period: 1}); n != 0 {
		t.Fatalf("expect 0 for unsubscribed symbol, got %d", n)
	}
}

func TestSymbolsReflectsIndexKeys(t *testing.T) {
	h := newTestHub()
	a, b := newDispatchConn(1), newDispatchConn(2)
	h.Register(a)
	h.Register(b)
	link(h, a, "m:600000")
	link(h, b, "m:600000", "m:000001") // 多连接同 symbol：索引键去重

	set := symSet(h)
	if len(set) != 2 || !set["m:600000"] || !set["m:000001"] {
		t.Fatalf("expect {m:600000, m:000001}, got %v", set)
	}
}

func TestSubsLimitPartialThenStop(t *testing.T) {
	h := newTestHub()
	a := newDispatchConn(1)
	a.Subs = domain.NewSubs(2) // 收紧上限验证部分接纳
	h.Register(a)

	link(h, a, "m:1", "m:2", "m:3") // 第 3 个超限：仅前 2 个入索引
	set := symSet(h)
	if len(set) != 2 || set["m:3"] {
		t.Fatalf("expect partial accept {m:1, m:2}, got %v", set)
	}
	if n := h.DispatchQuote("m:3", wsproto.QuotePush{Symbol: "m:3"}); n != 0 {
		t.Fatalf("expect over-limit symbol not dispatched, got %d", n)
	}
}
