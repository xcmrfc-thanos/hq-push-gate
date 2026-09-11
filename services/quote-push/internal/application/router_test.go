package application

import (
	"context"
	"testing"
	"time"

	hqv1 "github.com/hqpush/gate/packages/contract/go/hq/v1"
)

type fakeIndex struct {
	symGWs map[string][]string
	gwSlots map[string][]uint32
}

func (f *fakeIndex) SymbolGateways(_ context.Context, symKey string) ([]string, error) {
	return f.symGWs[symKey], nil
}

func (f *fakeIndex) GatewaySlots(_ context.Context, gw string) ([]uint32, error) {
	return f.gwSlots[gw], nil
}

func TestRouterFanoutGroupsBySlot(t *testing.T) {
	idx := &fakeIndex{
		symGWs: map[string][]string{
			"A_SHARE:600000": {"ws-b", "ws-a"},
			"A_SHARE:000001": {"ws-a"},
		},
		gwSlots: map[string][]uint32{
			"ws-a": {3, 1},
			"ws-b": {7},
		},
	}
	r := NewRouter(idx, time.Minute)
	ticks := []*hqv1.Tick{
		{Market: hqv1.Market_A_SHARE, Symbol: "600000", TimestampMs: 1},
		{Market: hqv1.Market_A_SHARE, Symbol: "000001", TimestampMs: 2},
	}
	targets, err := r.Resolve(context.Background(), []string{"A_SHARE:600000", "A_SHARE:000001"})
	if err != nil {
		t.Fatal(err)
	}
	batches := r.Fanout(ticks, targets)

	if len(batches) != 2 { // ws-a（两个 symbol 合并到槽位1）+ ws-b（600000 → 槽位7）
		t.Fatalf("want 2 targets, got %d: %+v", len(batches), batches)
	}
	// 单实例多槽位选最小槽位
	for tgt := range batches {
		if tgt.GatewayID == "ws-a" && tgt.GatewaySlot != 1 {
			t.Fatalf("ws-a should use min slot 1, got %d", tgt.GatewaySlot)
		}
	}
	// ws-a 同时持有两个 symbol
	wsA := batches[Target{GatewayID: "ws-a", GatewaySlot: 1}]
	if len(wsA) != 2 {
		t.Fatalf("ws-a batch should contain 2 ticks, got %d", len(wsA))
	}
}

func TestRouterSkipsGatewayWithoutSlots(t *testing.T) {
	idx := &fakeIndex{
		symGWs:  map[string][]string{"HK:00700": {"ws-x"}},
		gwSlots: map[string][]uint32{"ws-x": {}}, // 未持有槽位（扩缩容中）
	}
	r := NewRouter(idx, time.Minute)
	targets, err := r.Resolve(context.Background(), []string{"HK:00700"})
	if err != nil {
		t.Fatal(err)
	}
	if len(targets["HK:00700"]) != 0 {
		t.Fatalf("should skip gateway without slots, got %+v", targets)
	}
}
