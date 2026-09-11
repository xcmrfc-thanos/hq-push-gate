// Package domain 领域模型：连接订阅状态、帧限速、行情丢旧保新缓冲（纯逻辑，可单元测试）。
package domain

import (
	"sync"
	"time"

	"github.com/hqpush/gate/packages/contract/wsproto"
)

// Subs 单连接订阅集合， enforce 冻结上限（单连接 200 symbol）。
type Subs struct {
	mu  sync.RWMutex
	max int
	set map[string]struct{}
}

func NewSubs(max int) *Subs { return &Subs{max: max, set: make(map[string]struct{})} }

// Add 返回 false 表示超过上限。
func (s *Subs) Add(symKeys ...string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range symKeys {
		if _, ok := s.set[k]; !ok {
			if len(s.set) >= s.max {
				return false
			}
			s.set[k] = struct{}{}
		}
	}
	return true
}

// List 当前订阅集合快照（心跳续期 symgw 索引用）。
func (s *Subs) List() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.set))
	for k := range s.set {
		out = append(out, k)
	}
	return out
}

func (s *Subs) Remove(symKeys ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range symKeys {
		delete(s.set, k)
	}
}

// Has 是否已订阅。
func (s *Subs) Has(symKey string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.set[symKey]
	return ok
}

// All 当前订阅（副本）。
func (s *Subs) All() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.set))
	for k := range s.set {
		out = append(out, k)
	}
	return out
}

func (s *Subs) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.set)
}

// QuoteBuffer 行情丢旧保新缓冲：每个 symbol 只保留最新一条，
// 发送周期到期后批量刷出（docs/07 §5.1：行情消息允许丢旧保新）。
type QuoteBuffer struct {
	mu   sync.Mutex
	data map[string]wsproto.QuotePush
}

func NewQuoteBuffer() *QuoteBuffer { return &QuoteBuffer{data: make(map[string]wsproto.QuotePush)} }

func (b *QuoteBuffer) Push(q wsproto.QuotePush) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if cur, ok := b.data[q.Symbol]; ok && cur.TS >= q.TS {
		return
	}
	b.data[q.Symbol] = q
}

// Drain 取走全部待发送行情。
func (b *QuoteBuffer) Drain() []wsproto.QuotePush {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]wsproto.QuotePush, 0, len(b.data))
	for _, q := range b.data {
		out = append(out, q)
	}
	b.data = make(map[string]wsproto.QuotePush)
	return out
}

// FrameLimiter 滑动窗口帧限速（冻结契约：20 帧/s）。
type FrameLimiter struct {
	mu    sync.Mutex
	rate  int
	stamp []time.Time
}

func NewFrameLimiter(rate int) *FrameLimiter { return &FrameLimiter{rate: rate} }

// Allow 返回 false 表示超过窗口内帧速率上限。
func (l *FrameLimiter) Allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	windowStart := now.Add(-time.Second)
	keep := l.stamp[:0]
	for _, t := range l.stamp {
		if t.After(windowStart) {
			keep = append(keep, t)
		}
	}
	l.stamp = keep
	if len(l.stamp) >= l.rate {
		return false
	}
	l.stamp = append(l.stamp, now)
	return true
}

// KlinePeriods 单连接的 K 线周期订阅集合（"1"/"5"/"60"... 分钟字符串；B21 kline@1m）。
type KlinePeriods struct {
	mu  sync.RWMutex
	set map[string]struct{}
}

func NewKlinePeriods() *KlinePeriods {
	return &KlinePeriods{set: make(map[string]struct{})}
}

// Add 注册周期，返回 false 表示超过上限。
func (k *KlinePeriods) Add(periods ...string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, p := range periods {
		if _, ok := k.set[p]; !ok {
			if len(k.set) >= 8 {
				return false
			}
			k.set[p] = struct{}{}
		}
	}
	return true
}

// Has 是否订阅了该周期。
func (k *KlinePeriods) Has(period string) bool {
	k.mu.RLock()
	defer k.mu.RUnlock()
	// 注意：map[string]struct{} 用 m[key]==struct{}{} 判断对不存在的 key 恒真（零值即空结构体），
	// 必须用 ok 判定（B22 修复：此前 DispatchKline 周期过滤失效，未订阅周期也收 kline）。
	_, ok := k.set[period]
	return ok
}

// Remove 移除周期。
func (k *KlinePeriods) Remove(periods ...string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, p := range periods {
		delete(k.set, p)
	}
}

// Periods 当前已订阅周期列表（快照，复核器降级摘除用）。
func (k *KlinePeriods) Periods() []string {
	k.mu.RLock()
	defer k.mu.RUnlock()
	out := make([]string, 0, len(k.set))
	for p := range k.set {
		out = append(out, p)
	}
	return out
}
