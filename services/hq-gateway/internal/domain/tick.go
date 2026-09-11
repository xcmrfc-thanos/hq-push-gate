// Package domain 领域模型与标准化规则，不依赖 Kafka、Redis 和 Web 框架。
package domain

import (
	"fmt"
	"strings"
	"time"
)

// Market 冻结市场枚举，与 hqv1.Market 数值一致（docs/04 §1）。
type Market int32

const (
	MarketAShare Market = 0
	MarketHK     Market = 1
	MarketUS     Market = 2
	MarketCrypto Market = 3
)

var marketByName = map[string]Market{
	"A_SHARE": MarketAShare,
	"HK":      MarketHK,
	"US":      MarketUS,
	"CRYPTO":  MarketCrypto,
}

// ParseMarket 由字符串解析市场。
func ParseMarket(s string) (Market, error) {
	m, ok := marketByName[strings.ToUpper(strings.TrimSpace(s))]
	if !ok {
		return 0, fmt.Errorf("unknown market %q", s)
	}
	return m, nil
}

// String 市场字符串名。
func (m Market) String() string {
	for n, v := range marketByName {
		if v == m {
			return n
		}
	}
	return "UNKNOWN"
}

// Tick 标准化行情 tick（全链路统一携带 market）。
type Tick struct {
	Market      Market  `json:"market"`
	Symbol      string  `json:"symbol"`
	Exchange    string  `json:"exchange"`
	TimestampMS int64   `json:"timestamp_ms"`
	LastPrice   float64 `json:"last_price"`
	Open        float64 `json:"open"`
	High        float64 `json:"high"`
	Low         float64 `json:"low"`
	PreClose    float64 `json:"pre_close"`
	Volume      float64 `json:"volume"`
	Amount      float64 `json:"amount"`
}

// Validate 标准化校验：symbol 归一为大写，价格时间必须有效。
func (t *Tick) Validate() error {
	t.Symbol = strings.ToUpper(strings.TrimSpace(t.Symbol))
	if t.Symbol == "" {
		return fmt.Errorf("symbol required")
	}
	if t.TimestampMS <= 0 {
		t.TimestampMS = time.Now().UnixMilli()
	}
	if t.LastPrice <= 0 {
		return fmt.Errorf("last_price must be positive")
	}
	return nil
}

// Key 分区键 market:symbol。
func (t *Tick) Key() string { return t.Market.String() + ":" + t.Symbol }
