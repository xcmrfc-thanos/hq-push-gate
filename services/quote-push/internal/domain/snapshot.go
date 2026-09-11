// Package domain 领域模型，不依赖 Kafka、Redis 和 Web 框架。
package domain

import hqv1 "github.com/hqpush/gate/packages/contract/go/hq/v1"

// SymbolKey 行情路由键 "market:symbol"。
func SymbolKey(m hqv1.Market, symbol string) string {
	return m.String() + ":" + symbol
}

// Pct 相对昨收涨跌幅（百分比，保留两位）。
func Pct(last, preClose float64) float64 {
	if preClose <= 0 {
		return 0
	}
	p := (last - preClose) / preClose * 100
	return float64(int(p*100+0.5*sign(p))) / 100
}

func sign(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}
