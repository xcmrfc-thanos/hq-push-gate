// Package contract 定义冻结契约常量：Topic 名称、分区数、ID 语义与槽位规则。
// 冻结来源：docs/07-设计基线与验收口径.md、docs/04-数据模型与接口契约.md。
package contract

// Kafka Topic（冻结项）
const (
	TopicTickRaw       = "tick_raw"
	TopicRuleBcast     = "rule_bcast"
	TopicAlertEvent    = "alert_event"
	TopicSnapshotKline = "snapshot_kline"
	TopicWsPush        = "ws_push"
	TopicNotifyRetry   = "notify_retry"
	TopicDeadLetter    = "dead_letter"
)

// ws_push 固定槽位数与分区数一致；消息按 gateway_slot 作为分区键写入对应分区。
const (
	WsPushSlots    = 64
	AlertWindowSec = 72 * 3600 // 告警保留窗口 72h，与 alert_event 3d 保留对齐
)

// Redis Key（契约见 docs/04 §5）
const (
	RedisSnapPrefix    = "snap:"          // snap:{market}:{symbol}
	RedisConnPrefix    = "conn:"          // conn:{userId} -> gatewayId
	RedisGwSubsPrefix  = "gwsubs:"        // gwsubs:{gatewayId} -> {market}:{symbol} 集合
	RedisSymGwPrefix   = "symgw:"         // symgw:{market}:{symbol} -> gatewayId 集合（quote-push 路由索引）
	RedisSlotLeasePfx  = "gwslease:"      // gwslease:{slot} -> {gatewayId,version,fencingToken} 带版本租约
	RedisGwSlotsPfx    = "gwidslots:"     // gwidslots:{gatewayId} -> 该实例持有的槽位集合
	RedisCoolPrefix    = "cool:"          // cool:{ruleId}
	RedisKlineCurPfx   = "kline:cur:"     // kline:cur:{market}:{symbol}:{period}
	RedisWsPushOffset  = "offset:ws_push" // offset:ws_push:{slot} -> 已消费分区 offset
)

// SymbolKey 返回 "market:symbol" 标准键（订阅集合与路由索引用）。
func SymbolKey(market, symbol string) string { return market + ":" + symbol }

// EventID：一次规则命中的全局事件，唯一键 rule_id + rule_version + trigger_window。
func EventID(ruleID, ruleVersion, triggerWindow int64) string {
	return "r" + itoa(ruleID) + "-v" + itoa(ruleVersion) + "-w" + itoa(triggerWindow)
}

// DeliveryID：一次事件面向一个用户的投递，唯一键 event_id + user_id。
func DeliveryID(eventID string, userID int64) string {
	return eventID + ":u" + itoa(userID)
}

func itoa(v int64) string {
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
