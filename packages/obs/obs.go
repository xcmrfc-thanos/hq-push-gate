// Package obs 提供统一的结构化日志、Prometheus 指标与健康检查（docs/08 §7.1，U0 基线）。
package obs

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NewLogger 返回 JSON 结构化 stdout logger。
func NewLogger(service string) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})).With("service", service)
}

// Metrics 服务级 Prometheus 注册表（关闭 Go 运行时高基数收集可按需裁剪）。
type Metrics struct {
	Registry *prometheus.Registry
}

func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return &Metrics{Registry: reg}
}

// Counter 声明并注册一个 Counter。
func (m *Metrics) Counter(name, help string, labels ...string) *prometheus.CounterVec {
	c := prometheus.NewCounterVec(prometheus.CounterOpts{Name: name, Help: help}, labels)
	m.Registry.MustRegister(c)
	return c
}

// Histogram 声明并注册一个 Histogram。
func (m *Metrics) Histogram(name, help string, buckets []float64, labels ...string) *prometheus.HistogramVec {
	h := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: name, Help: help, Buckets: buckets}, labels)
	m.Registry.MustRegister(h)
	return h
}

// Gauge 声明并注册一个 Gauge。
func (m *Metrics) Gauge(name, help string, labels ...string) *prometheus.GaugeVec {
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: name, Help: help}, labels)
	m.Registry.MustRegister(g)
	return g
}

// Register 在既有 mux 上注册 /healthz（存活）、/readyz（readiness）与 /metrics。
// readiness 由调用方通过 ready 回调控制依赖检查结果。
func (m *Metrics) Register(mux *http.ServeMux, ready func(context.Context) bool) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if ready(r.Context()) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ready"))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not ready"))
	})
	mux.Handle("GET /metrics", promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{}))
}
