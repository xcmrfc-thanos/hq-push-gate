// Package application 用例编排：连接 Hub、订阅索引、槽位租约与 ws_push 分区消费。
package application

import (
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/hqpush/gate/packages/contract/wsproto"
	"github.com/hqpush/gate/services/ws-gateway/internal/domain"
)

// Conn 一条客户端连接的服务端状态。
type Conn struct {
	UserID    int64
	Subs      *domain.Subs
	Quotes    *domain.QuoteBuffer
	AlertCh   chan wsproto.AlertPush
	KlineCh   chan wsproto.KlinePush // 周期闭合 K 线（B21，低频：每 symbol 每周期 1 条）
	KlineSubs *domain.KlinePeriods   // K 线周期订阅（channels kline@1m 等）
	SysCh     chan wsproto.SysData
	Done      chan struct{}
	CloseOnce sync.Once
	Ent       atomic.Pointer[domain.Entitlement] // B22 行情权益快照（atomic 整值替换：建连 Store，复核器周期更新；读用 Load）
}

// CloseOnceClose 幂等关闭连接。
func (c *Conn) CloseOnceClose() { c.CloseOnce.Do(func() { close(c.Done) }) }

// Hub 本实例连接注册表、订阅反向索引与消息分发。
// symIndex 为进程内扇出索引（symKey -> 订阅连接）：Dispatch 由"全连接扫描"降为
// "仅命中连接"——20 万连接目标下逐 tick 扫描全部连接是不可接受的 CPU 开销。
type Hub struct {
	mu       sync.RWMutex
	conns    map[int64]map[*Conn]struct{}
	symIndex map[string]map[*Conn]struct{}
	total    *TotalGauge
}

func NewHub(total *TotalGauge) *Hub {
	return &Hub{
		conns:    make(map[int64]map[*Conn]struct{}),
		symIndex: make(map[string]map[*Conn]struct{}),
		total:    total,
	}
}

func (h *Hub) Register(c *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set, ok := h.conns[c.UserID]
	if !ok {
		set = make(map[*Conn]struct{})
		h.conns[c.UserID] = set
	}
	set[c] = struct{}{}
	h.total.Add(1)
}

func (h *Hub) Unregister(c *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if set, ok := h.conns[c.UserID]; ok {
		delete(set, c)
		if len(set) == 0 {
			delete(h.conns, c.UserID)
		}
	}
	// 反向索引摘除：调用方保证此时该连接的读循环已退出，Subs 不再变更
	for _, k := range c.Subs.List() {
		if conns, ok := h.symIndex[k]; ok {
			delete(conns, c)
			if len(conns) == 0 {
				delete(h.symIndex, k)
			}
		}
	}
	h.total.Add(-1)
}

// LinkSym 反向索引登记：serveConn 在 Subs 实际接纳后调用，键与 Subs 精确一致。
func (h *Hub) LinkSym(c *Conn, symKeys ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, k := range symKeys {
		set, ok := h.symIndex[k]
		if !ok {
			set = make(map[*Conn]struct{})
			h.symIndex[k] = set
		}
		set[c] = struct{}{}
	}
}

// UnlinkSym 反向索引摘除（幂等，未登记键安全）。
func (h *Hub) UnlinkSym(c *Conn, symKeys ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, k := range symKeys {
		if conns, ok := h.symIndex[k]; ok {
			delete(conns, c)
			if len(conns) == 0 {
				delete(h.symIndex, k)
			}
		}
	}
}

// Count 当前实例连接数。
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	n := 0
	for _, set := range h.conns {
		n += len(set)
	}
	return n
}

// UserConns 返回某用户的全部连接（快照）。
func (h *Hub) UserConns(userID int64) []*Conn {
	h.mu.RLock()
	defer h.mu.RUnlock()
	set := h.conns[userID]
	out := make([]*Conn, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	return out
}

// DispatchQuote 行情分发：仅投给反向索引中订阅了该 symbol 的连接（丢旧保新缓冲）。
// 返回实际投递的连接数。
func (h *Hub) DispatchQuote(symKey string, q wsproto.QuotePush) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	n := 0
	for c := range h.symIndex[symKey] {
		c.Quotes.Push(q)
		n++
	}
	return n
}

// DispatchKline 周期闭合 K 线分发：反向索引命中 symbol 的连接里再按周期过滤。
// 返回实际投递的连接数。
func (h *Hub) DispatchKline(symKey string, periodMin int32, k wsproto.KlinePush) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	n := 0
	period := strconv.FormatInt(int64(periodMin), 10)
	for c := range h.symIndex[symKey] {
		if !c.KlineSubs.Has(period) {
			continue
		}
		select {
		case c.KlineCh <- k:
			n++
		default: // K 线缓冲满：丢旧保新语义下允许跳过（下周期即有新 bar）
		}
	}
	return n
}

// DeliverAlert 告警分发：高优先级通道（独立于行情缓冲，docs/07 §5.1）。
// 用户不在线/不在本实例返回 false（记录保持 PENDING，等待补拉）。
func (h *Hub) DeliverAlert(userID int64, a wsproto.AlertPush) bool {
	for _, c := range h.UserConns(userID) {
		select {
		case c.AlertCh <- a:
			return true
		default:
			// 告警队列满：丢弃并保留 PENDING，客户端重连后可补拉（服务端不丢）
			return false
		}
	}
	return false
}

// UserIDs 当前在线用户列表（快照）。
func (h *Hub) UserIDs() []int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]int64, 0, len(h.conns))
	for uid := range h.conns {
		out = append(out, uid)
	}
	return out
}

// Symbols 本实例订阅的 symbol 全集（反向索引键；心跳续期 symgw/gwsubs 索引用）。
func (h *Hub) Symbols() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]string, 0, len(h.symIndex))
	for k := range h.symIndex {
		out = append(out, k)
	}
	return out
}

// DrainAll 优雅下线：通知全部连接重连。
func (h *Hub) DrainAll(notify func(c *Conn)) {
	h.mu.RLock()
	var all []*Conn
	for _, set := range h.conns {
		for c := range set {
			all = append(all, c)
		}
	}
	h.mu.RUnlock()
	for _, c := range all {
		notify(c)
	}
}
