package application

import (
	"testing"

	hqv1 "github.com/hqpush/gate/packages/contract/go/hq/v1"
)

func mkTick(market hqv1.Market, symbol string, ts int64) *hqv1.Tick {
	return &hqv1.Tick{Market: market, Symbol: symbol, TimestampMs: ts, LastPrice: 10}
}

func TestMergerKeepsLatestPerSymbol(t *testing.T) {
	m := NewMerger()
	m.Add(mkTick(hqv1.Market_A_SHARE, "600000", 100), "t1")
	m.Add(mkTick(hqv1.Market_A_SHARE, "600000", 200), "t2") // 更新
	m.Add(mkTick(hqv1.Market_A_SHARE, "600000", 150), "t3") // 旧于当前，应丢弃
	m.Add(mkTick(hqv1.Market_A_SHARE, "000001", 300), "t1")

	if got := m.Len(); got != 2 {
		t.Fatalf("want 2 pending, got %d", got)
	}
	out := m.Flush()
	if len(out) != 2 {
		t.Fatalf("want 2 flushed, got %d", len(out))
	}
	for _, it := range out {
		if it.Tick().Symbol == "600000" {
			if it.Tick().TimestampMs != 200 {
				t.Fatalf("600000 should keep latest ts=200, got %d", it.Tick().TimestampMs)
			}
			if it.TraceID() != "t2" {
				t.Fatalf("600000 trace should follow latest tick (t2), got %q", it.TraceID())
			}
		}
	}
	if m.Len() != 0 {
		t.Fatal("flush should clear buffer")
	}
}

func TestMergerFlushClearsThenAccumulates(t *testing.T) {
	m := NewMerger()
	m.Add(mkTick(hqv1.Market_HK, "00700", 1), "")
	_ = m.Flush()
	m.Add(mkTick(hqv1.Market_HK, "00700", 2), "t9")
	out := m.Flush()
	if len(out) != 1 || out[0].Tick().TimestampMs != 2 {
		t.Fatalf("unexpected second flush: %+v", out)
	}
	if out[0].TraceID() != "t9" {
		t.Fatalf("trace should be t9, got %q", out[0].TraceID())
	}
}
