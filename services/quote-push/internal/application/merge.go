// Package application 用例编排：行情合并（丢旧保新）与 ws_push 槽位路由。
package application

import (
	"sync"

	hqv1 "github.com/hqpush/gate/packages/contract/go/hq/v1"
	"github.com/hqpush/gate/services/quote-push/internal/domain"
)

// item 合并缓冲条目：tick + 最近一次写入的 trace_id（trace 尽力传播，docs/08 §7.1.3）。
type item struct {
	tick    *hqv1.Tick
	traceID string
}

// Tick 条目内的 tick。
func (it *item) Tick() *hqv1.Tick { return it.tick }

// TraceID 条目关联的链路 ID（可能为空）。
func (it *item) TraceID() string { return it.traceID }

// Merger 合并窗口内的 tick：每个 symbol 只保留最新一条（丢旧保新，docs/07 §5.1）。
// 纯内存逻辑，可单元测试。
type Merger struct {
	mu     sync.Mutex
	latest map[string]*item
}

func NewMerger() *Merger {
	return &Merger{latest: make(map[string]*item)}
}

// Add 加入一个 tick；旧于当前缓存的 tick 被丢弃。traceID 为该消息链路的追踪 ID。
func (m *Merger) Add(t *hqv1.Tick, traceID string) {
	if t == nil || t.Symbol == "" {
		return
	}
	key := domain.SymbolKey(t.Market, t.Symbol)
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.latest[key]; ok && cur.tick.TimestampMs >= t.TimestampMs {
		return
	}
	m.latest[key] = &item{tick: t, traceID: traceID}
}

// Flush 取走当前合并结果（窗口 flush 时调用）。
func (m *Merger) Flush() []*item {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*item, 0, len(m.latest))
	for _, it := range m.latest {
		out = append(out, it)
	}
	m.latest = make(map[string]*item)
	return out
}

// Len 当前待合并数量。
func (m *Merger) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.latest)
}
