package application

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/hqpush/gate/packages/contract"
)

// LeaseRecord 槽位租约（带版本与 fencing token，docs/07 §5.2）。
type LeaseRecord struct {
	GatewayID    string `json:"gateway_id"`
	Version      int64  `json:"version"`
	FencingToken int64  `json:"fencing_token"`
}

// leaseScript 原子租约操作：
// KEYS[1]=gwslease:{slot}
// ARGV: gateway_id, ttl_ms, now_version(新版本), fencing_token
// 返回: 1=acquired(原本空闲) 2=renewed(本实例持有) 0=held(他实例持有)
var leaseScript = redis.NewScript(`
local cur = redis.call('GET', KEYS[1])
if cur == false then
  redis.call('SET', KEYS[1], ARGV[3], 'PX', tonumber(ARGV[2]))
  return 1
end
local rec = cjson.decode(cur)
if rec["gateway_id"] == ARGV[1] then
  redis.call('SET', KEYS[1], ARGV[3], 'PX', tonumber(ARGV[2]))
  return 2
end
return 0
`)

// LeaseManager 管理本实例的槽位租约：acquire/renew/lose 生命周期，
// 状态变化通过回调驱动分区消费的启停（禁止动态创建 Topic，仅槽位迁移）。
type LeaseManager struct {
	r         *redis.Client
	gatewayID string
	slots     []uint32
	ttl       time.Duration

	OnAcquire func(ctx context.Context, slot uint32)
	OnLose    func(ctx context.Context, slot uint32)

	log    *slog.Logger
	held   map[uint32]bool
	ver    int64
	fencing int64
}

func NewLeaseManager(r *redis.Client, gatewayID string, slots []uint32, ttl time.Duration, log *slog.Logger) *LeaseManager {
	return &LeaseManager{
		r: r, gatewayID: gatewayID, slots: slots, ttl: ttl, log: log,
		held: make(map[uint32]bool), ver: time.Now().UnixNano(), fencing: time.Now().UnixNano(),
	}
}

// Run 周期续租，阻塞直到 ctx 取消。首次调用立即执行一轮。
func (m *LeaseManager) Run(ctx context.Context, renew time.Duration) {
	m.tick(ctx)
	ticker := time.NewTicker(renew)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			m.releaseAll(context.Background())
			return
		case <-ticker.C:
			m.tick(ctx)
		}
	}
}

func (m *LeaseManager) tick(ctx context.Context) {
	for _, slot := range m.slots {
		key := contract.RedisSlotLeasePfx + itoa64(int64(slot))
		rec := LeaseRecord{GatewayID: m.gatewayID, Version: m.ver, FencingToken: m.fencing}
		val, _ := json.Marshal(rec)
		res, err := leaseScript.Run(ctx, m.r, []string{key},
			m.gatewayID, m.ttl.Milliseconds(), string(val)).Int()
		if err != nil {
			m.log.Error("lease script failed", "slot", slot, "err", err)
			continue
		}
		switch res {
		case 1: // 新获得
			m.held[slot] = true
			m.log.Info("slot lease acquired", "slot", slot)
			if _, err := m.r.SAdd(ctx, contract.RedisGwSlotsPfx+m.gatewayID, slot).Result(); err != nil {
				m.log.Error("register slot failed", "slot", slot, "err", err)
			}
			if m.OnAcquire != nil {
				m.OnAcquire(ctx, slot)
			}
		case 2: // 续期
			if !m.held[slot] {
				// 实例重启后 redis 中 gwidslots 可能残留或缺失，校正
				m.held[slot] = true
				if m.OnAcquire != nil {
					m.OnAcquire(ctx, slot)
				}
			}
		default: // 他实例持有
			if m.held[slot] {
				m.held[slot] = false
				m.log.Warn("slot lease lost", "slot", slot)
				_, _ = m.r.SRem(ctx, contract.RedisGwSlotsPfx+m.gatewayID, slot).Result()
				if m.OnLose != nil {
					m.OnLose(ctx, slot)
				}
			}
		}
	}
}

// Held 当前持有槽位。
func (m *LeaseManager) Held() []uint32 {
	out := make([]uint32, 0, len(m.held))
	for s, ok := range m.held {
		if ok {
			out = append(out, s)
		}
	}
	return out
}

// releaseAll 进程退出时主动释放（drain 语义：槽位尽快被冗余实例接管）。
func (m *LeaseManager) releaseAll(ctx context.Context) {
	for slot, ok := range m.held {
		if !ok {
			continue
		}
		key := contract.RedisSlotLeasePfx + itoa64(int64(slot))
		cur, err := m.r.Get(ctx, key).Result()
		if err == nil {
			var rec LeaseRecord
			if json.Unmarshal([]byte(cur), &rec) == nil && rec.GatewayID == m.gatewayID {
				_ = m.r.Del(ctx, key).Err()
			}
		}
		_, _ = m.r.SRem(ctx, contract.RedisGwSlotsPfx+m.gatewayID, slot).Result()
		if m.OnLose != nil {
			m.OnLose(ctx, slot)
		}
		m.held[slot] = false
	}
}

func itoa64(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
