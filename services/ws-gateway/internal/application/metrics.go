package application

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// TotalGauge 连接数仪表（并发安全累加）。
type TotalGauge struct {
	mu sync.Mutex
	g  prometheus.Gauge
	v  int
}

func NewTotalGauge(g prometheus.Gauge) *TotalGauge { return &TotalGauge{g: g} }

func (t *TotalGauge) Add(n int) {
	t.mu.Lock()
	t.v += n
	t.g.Set(float64(t.v))
	t.mu.Unlock()
}

func (t *TotalGauge) Value() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.v
}
