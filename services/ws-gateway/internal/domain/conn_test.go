package domain

import (
	"testing"
	"time"

	"github.com/hqpush/gate/packages/contract/wsproto"
)

func TestSubsCapEnforced(t *testing.T) {
	s := NewSubs(2)
	if !s.Add("A_SHARE:600000") {
		t.Fatal("first add should succeed")
	}
	if !s.Add("A_SHARE:000001") {
		t.Fatal("second add should succeed")
	}
	if s.Add("HK:00700") {
		t.Fatal("third add over cap should fail")
	}
	if s.Len() != 2 {
		t.Fatalf("len should stay 2, got %d", s.Len())
	}
	// 重复添加不受上限影响
	if !s.Add("A_SHARE:600000") {
		t.Fatal("duplicate add should be idempotent")
	}
	s.Remove("A_SHARE:600000")
	if s.Len() != 1 || !s.Has("A_SHARE:000001") {
		t.Fatal("remove should free capacity")
	}
}

func TestQuoteBufferDropsStale(t *testing.T) {
	b := NewQuoteBuffer()
	b.Push(wsproto.QuotePush{Symbol: "A_SHARE:600000", Last: 10, TS: 100})
	b.Push(wsproto.QuotePush{Symbol: "A_SHARE:600000", Last: 11, TS: 200})
	b.Push(wsproto.QuotePush{Symbol: "A_SHARE:600000", Last: 12, TS: 150}) // 旧于当前
	out := b.Drain()
	if len(out) != 1 || out[0].Last != 11 {
		t.Fatalf("want keep latest ts=200 last=11, got %+v", out)
	}
	if got := b.Drain(); len(got) != 0 {
		t.Fatal("drain should empty buffer")
	}
}

func TestFrameLimiter(t *testing.T) {
	l := NewFrameLimiter(3)
	now := time.Now()
	for i := 0; i < 3; i++ {
		if !l.Allow(now) {
			t.Fatalf("frame %d should be allowed", i)
		}
	}
	if l.Allow(now) {
		t.Fatal("4th frame within 1s should be rejected")
	}
	if !l.Allow(now.Add(1100 * time.Millisecond)) {
		t.Fatal("after window slide should be allowed")
	}
}
