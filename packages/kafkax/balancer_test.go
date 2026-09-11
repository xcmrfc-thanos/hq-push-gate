package kafkax

import (
	"testing"

	"github.com/segmentio/kafka-go"

	"github.com/hqpush/gate/packages/contract"
)

// ids 生成 0..n-1 的分区 ID 列表（kafka-go writer 传给 Balance 的真实形态）。
func ids(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

func TestSlotAwareBalancerExactPartition(t *testing.T) {
	b := NewSlotAwareBalancer()
	for slot := uint32(0); slot < contract.WsPushSlots; slot++ {
		msg := kafka.Message{Key: []byte(itoaSlot(slot))}
		if got := b.Balance(msg, ids(contract.WsPushSlots)...); int(slot) != got {
			t.Fatalf("slot %d should map to partition %d, got %d", slot, slot, got)
		}
	}
}

func TestSlotAwareBalancerFallbackHash(t *testing.T) {
	b := NewSlotAwareBalancer()
	msg := kafka.Message{Key: []byte("A_SHARE:600000")}
	got := b.Balance(msg, ids(contract.WsPushSlots)...)
	if got < 0 || got >= contract.WsPushSlots {
		t.Fatalf("partition out of range: %d", got)
	}
	// 同 key 稳定
	if again := b.Balance(msg, ids(contract.WsPushSlots)...); again != got {
		t.Fatal("same key should map to same partition")
	}
	// 非 ws_push 槽位数（如 tick_raw 24 分区）走 fallback 哈希
	if got24 := b.Balance(msg, ids(24)...); got24 < 0 || got24 >= 24 {
		t.Fatalf("fallback partition out of range: %d", got24)
	}
	// 非数字 key（如 gateway_id）不得映射到槽位分区
	gwKey := b.Balance(kafka.Message{Key: []byte("ws-xc-xps15")}, ids(contract.WsPushSlots)...)
	if gwKey < 0 || gwKey >= contract.WsPushSlots {
		t.Fatalf("gateway key partition out of range: %d", gwKey)
	}
}

func itoaSlot(v uint32) string {
	if v == 0 {
		return "0"
	}
	var buf [10]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
