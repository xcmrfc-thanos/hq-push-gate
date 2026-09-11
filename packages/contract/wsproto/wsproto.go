// Package wsproto 定义客户端 WebSocket JSON 协议（docs/04 §7，冻结契约）。
package wsproto

import "encoding/json"

// 消息类型
const (
	TypeSub   = "sub"
	TypeUnsub = "unsub"
	TypePing  = "ping"
	TypePong  = "pong"
	TypeQuote = "quote"
	TypeKline = "kline"
	TypeAlert = "alert"
	TypeAck   = "ack"
	TypeSys   = "sys"
)

// 客户端消息
type ClientMsg struct {
	Type    string   `json:"type"`
	Symbols []string `json:"symbols,omitempty"` // "A_SHARE:600000" 格式
	Channel []string `json:"channels,omitempty"`
	// Ack
	DeliveryID string `json:"deliveryId,omitempty"`
}

// 服务端消息统一信封
type ServerMsg struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// QuotePush 行情推送条目（docs/04 §7：{symbol,last,pct,vol,ts}）
type QuotePush struct {
	Symbol string  `json:"symbol"`           // "A_SHARE:600000"
	Last   float64 `json:"last"`
	Pct    float64 `json:"pct"` // 相对昨收涨跌幅百分比
	Vol    float64 `json:"vol"`
	TS     int64   `json:"ts"` // 毫秒时间戳
}

// AlertPush 告警推送条目（docs/04 §7：{deliveryId,eventId,ruleId,symbol,title,detail,ts}）
type AlertPush struct {
	DeliveryID string `json:"deliveryId"`
	EventID    string `json:"eventId"`
	RuleID     int64  `json:"ruleId"`
	Symbol     string `json:"symbol"`
	Title      string `json:"title"`
	Detail     string `json:"detail"`
	TS         int64  `json:"ts"`
}

// KlinePush 周期 K 线闭合推送（docs/04 §7 kline 帧；B21 kline@1m，字段对齐 proto Kline）。
type KlinePush struct {
	Symbol  string  `json:"symbol"`    // "A_SHARE:600000"
	Period  int32   `json:"period"`    // 分钟：1/5/15/30/60/1440（亚分钟留待契约扩展）
	BeginTS int64   `json:"begin_ts"`  // 窗口起始毫秒
	Open    float64 `json:"open"`
	High    float64 `json:"high"`
	Low     float64 `json:"low"`
	Close   float64 `json:"close"`
	Volume  float64 `json:"volume"`
	Amount  float64 `json:"amount"`
}

// SysData 系统通知（如缩容 drain 引导重连）
type SysData struct {
	Code string `json:"code"`
}

// Encode 服务端消息编码。
func Encode(typ string, data any) ([]byte, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return json.Marshal(ServerMsg{Type: typ, Data: raw})
}
