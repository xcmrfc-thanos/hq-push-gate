package application

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"time"

	hqv1 "github.com/hqpush/gate/packages/contract/go/hq/v1"
	"github.com/hqpush/gate/services/quote-push/internal/domain"
)

// RouteIndex 路由索引接口（Redis 适配实现）：
// symgw:{market}:{symbol} -> 订阅该 symbol 的 gateway 集合；
// gwidslots:{gatewayId}  -> 该实例租约持有的槽位集合。
type RouteIndex interface {
	SymbolGateways(ctx context.Context, symKey string) ([]string, error)
	GatewaySlots(ctx context.Context, gatewayID string) ([]uint32, error)
}

// Target 一个投递目标：实例与其持有的某个槽位。
type Target struct {
	GatewayID  string
	GatewaySlot uint32
}

// Router 将合并后的 tick 按订阅索引扇出到 ws_push 目标槽位。
// 同一 symbol 多实例订阅时每个实例投递一份；单实例多槽位只选最小槽位（实例消费其全部槽位分区）。
type Router struct {
	idx   RouteIndex
	cache *routeCache
}

func NewRouter(idx RouteIndex, ttl time.Duration) *Router {
	return &Router{idx: idx, cache: newRouteCache(ttl)}
}

// Resolve 返回 symKey 的投递目标集合。
func (r *Router) Resolve(ctx context.Context, symKeys []string) (map[string][]Target, error) {
	out := make(map[string][]Target, len(symKeys))
	for _, sym := range symKeys {
		gws, err := r.gatewayIDs(ctx, sym)
		if err != nil {
			return nil, err
		}
		targets := make([]Target, 0, len(gws))
		for _, gw := range gws {
			slots, err := r.gatewaySlots(ctx, gw)
			if err != nil {
				return nil, err
			}
			if len(slots) == 0 {
				continue // 实例暂未持有槽位（扩缩容中），跳过
			}
			targets = append(targets, Target{GatewayID: gw, GatewaySlot: slots[0]})
		}
		out[sym] = targets
	}
	return out, nil
}

// Fanout 按 Resolve 结果将 ticks 分组为 slot -> ticks。
func (r *Router) Fanout(ticks []*hqv1.Tick, targets map[string][]Target) map[Target][]*hqv1.Tick {
	out := make(map[Target][]*hqv1.Tick)
	for _, t := range ticks {
		for _, tgt := range targets[domain.SymbolKey(t.Market, t.Symbol)] {
			out[tgt] = append(out[tgt], t)
		}
	}
	return out
}

func (r *Router) gatewayIDs(ctx context.Context, sym string) ([]string, error) {
	if v, ok := r.cache.get(sym); ok {
		return v, nil
	}
	gws, err := r.idx.SymbolGateways(ctx, sym)
	if err != nil {
		return nil, err
	}
	sort.Strings(gws)
	r.cache.set(sym, gws)
	return gws, nil
}

func (r *Router) gatewaySlots(ctx context.Context, gw string) ([]uint32, error) {
	key := "\x00gw:" + gw
	if v, ok := r.cache.get(key); ok {
		return toSlots(v), nil
	}
	slots, err := r.idx.GatewaySlots(ctx, gw)
	if err != nil {
		return nil, err
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i] < slots[j] })
	vals := make([]string, len(slots))
	for i, s := range slots {
		vals[i] = strconv.FormatUint(uint64(s), 10)
	}
	r.cache.set(key, vals)
	return slots, nil
}

func toSlots(vals []string) []uint32 {
	out := make([]uint32, 0, len(vals))
	for _, v := range vals {
		if n, err := strconv.ParseUint(v, 10, 32); err == nil {
			out = append(out, uint32(n))
		}
	}
	return out
}

// routeCache 短 TTL 缓存，吸收高频扇出下的 Redis 读放大。
type routeCache struct {
	mu   sync.RWMutex
	ttl  time.Duration
	data map[string]entry
}

type entry struct {
	vals   []string
	expires time.Time
}

func newRouteCache(ttl time.Duration) *routeCache {
	return &routeCache{ttl: ttl, data: make(map[string]entry)}
}

func (c *routeCache) get(key string) ([]string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.data[key]
	if !ok || time.Now().After(e.expires) {
		return nil, false
	}
	return e.vals, true
}

func (c *routeCache) set(key string, vals []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = entry{vals: vals, expires: time.Now().Add(c.ttl)}
}


