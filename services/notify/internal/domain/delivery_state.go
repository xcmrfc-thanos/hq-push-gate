// Package domain 领域模型：告警投递状态机与幂等规则，不依赖 Kafka、Redis 和 Web 框架。
package domain

import "time"

// DeliveryStatus 投递状态机：PENDING -> SENT -> ACKED；超窗未 ACK -> EXPIRED。
// （docs/07 §6：notify 消费后幂等落库 PENDING，推送后推进状态，客户端 ACK 置 ACKED）
const (
	StatusPending = "PENDING"
	StatusSent    = "SENT"
	StatusAcked   = "ACKED"
	StatusExpired = "EXPIRED"
)

// CanTransition 状态迁移合法性。
func CanTransition(from, to string) bool {
	switch from {
	case StatusPending:
		return to == StatusSent || to == StatusAcked || to == StatusExpired
	case StatusSent:
		return to == StatusAcked || to == StatusExpired
	default:
		return false // ACKED/EXPIRED 为终态
	}
}

// AlertRecord alert-inbox 落库模型（alert_record 表）。
type AlertRecord struct {
	DeliveryID string
	EventID    string
	RuleID     int64
	UserID     int64
	Market     string
	Symbol     string
	Title      string
	TriggerAt  time.Time
	Status     string
	CursorID   int64
}

// Pullable 未 ACK 且未过期的投递可被补拉（docs/04 §6 /alerts）。
func Pullable(r *AlertRecord, now time.Time, window time.Duration) bool {
	if r.Status != StatusPending && r.Status != StatusSent {
		return false
	}
	return now.Sub(r.TriggerAt) < window
}

// RuleStat 单规则投递聚合（B28 条件质量报表：ACK 率 = acked/总投递；0 ACK 高触发 = 噪声规则信号）。
type RuleStat struct {
	RuleID  int64  `json:"rule_id"`
	Market  string `json:"market"`
	Symbol  string `json:"symbol"`
	Pending int    `json:"pending"`
	Sent    int    `json:"sent"`
	Acked   int    `json:"acked"`
	Expired int    `json:"expired"`
}
