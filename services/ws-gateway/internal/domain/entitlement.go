// 行情权益（B22，docs/10 §3 行情权益矩阵 + ADR-040）：按 user_vip.plan_type 决定
// quote 合并帧差频与 kline 周期档位。纯逻辑，可单元测试。
//
// 档位口径（与 ADR-041 的 3s/10s 亚分钟口径对齐）：
//   - free：quote 合并帧 10s；kline 仅 kline@1m
//   - vip1~vip3：quote 合并帧 3s（quote@3s 默认契约）；kline 全周期（1m~1d）
package domain

import "time"

const (
	PlanFree = "free"

	// QuoteIntervalFree/Vip quote 合并帧差频（writeLoop ticker 周期，丢旧保新天然支持）。
	QuoteIntervalFree = 10 * time.Second
	QuoteIntervalVip  = 3 * time.Second

	// KlineFreeMaxPeriod free 可订阅的 kline 周期上限（分钟）；>1m 档位仅 vip。
	KlineFreeMaxPeriod = int32(1)
)

// Entitlement 一条连接的行情权益快照（建连时读取，复核器周期更新）。
type Entitlement struct {
	PlanType      string
	QuoteInterval time.Duration
}

// NewEntitlement 按套餐构造权益（白名单 vip1~vip3；未知/空按 free 兜底——安全向低配）。
func NewEntitlement(planType string) *Entitlement {
	switch planType {
	case "vip1", "vip2", "vip3":
		return &Entitlement{PlanType: planType, QuoteInterval: QuoteIntervalVip}
	default:
		return &Entitlement{PlanType: PlanFree, QuoteInterval: QuoteIntervalFree}
	}
}

// IsVip 是否为付费档（vip1~vip3）。
func (e *Entitlement) IsVip() bool { return e.PlanType != PlanFree }

// KlineAllowed 该档位是否允许订阅 periodMin 的 kline 周期。
func (e *Entitlement) KlineAllowed(periodMin int32) bool {
	if e.IsVip() {
		return true
	}
	return periodMin <= KlineFreeMaxPeriod
}
