package kafkax

import (
	"strconv"
	"strings"

	"github.com/segmentio/kafka-go"

	"github.com/hqpush/gate/packages/contract"
)

// SlotAwareBalancer ws_push 专用分区器：key 为槽位号时精确写入对应分区，
// 其余 key 按 FNV-1a 哈希（等价 kafka.Hash 的默认行为）。
//
// 注意：kafka-go writer 传给 Balance 的是"分区 ID 列表"（0..n-1），不是分区数；
// 此处以"列表长度==槽位数且含该槽位 ID"判定可精确映射（历史实现对数量判定，路由失效）。
type SlotAwareBalancer struct{ fallback kafka.Balancer }

func NewSlotAwareBalancer() *SlotAwareBalancer { return &SlotAwareBalancer{fallback: &kafka.Hash{}} }

func (b *SlotAwareBalancer) Balance(msg kafka.Message, partitions ...int) int {
	if len(partitions) == contract.WsPushSlots {
		if slot, err := strconv.ParseUint(strings.TrimSpace(string(msg.Key)), 10, 32); err == nil {
			for _, p := range partitions {
				if int(slot) == p {
					return p
				}
			}
		}
	}
	return b.fallback.Balance(msg, partitions...)
}
