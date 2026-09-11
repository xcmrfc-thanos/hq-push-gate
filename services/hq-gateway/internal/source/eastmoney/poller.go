package eastmoney

import (
	"context"
	"log/slog"
	"math/rand"
	"time"

	"github.com/hqpush/gate/services/hq-gateway/internal/source"
)

// Poller 轮询东财快照源：固定周期拉取 → 标准化 → 交给 IngestService。
// 防封 IP 口径（docs/09 §5.1）：请求间最小间隔 + 失败指数退避（base*2^n + 抖动，封顶 max）。
// tick 接受/拒绝统一由 application.IngestService 计数，源层不重复埋点。
type Poller struct {
	client      *Client
	ingest      source.Ingest
	log         *slog.Logger
	symbols     []string
	interval    time.Duration // 正常轮询周期
	minGap      time.Duration // 相邻 HTTP 请求最小间隔（频控）
	backoff     func(failures int) time.Duration
	maxBatch    int  // 单次请求 secid 数上限
	sessionGate bool // true 时仅在 A 股交易时段轮询
}

// NewPoller 构造；backoffBase/BackoffMax 控制退避曲线。
func NewPoller(client *Client, ingest source.Ingest, log *slog.Logger, symbols []string,
	interval, minGap, backoffBase, backoffMax time.Duration, maxBatch int, sessionGate bool) *Poller {
	return &Poller{
		client:      client,
		ingest:      ingest,
		log:         log,
		symbols:     symbols,
		interval:    interval,
		minGap:      minGap,
		backoff:     backoffFn(backoffBase, backoffMax),
		maxBatch:    maxBatch,
		sessionGate: sessionGate,
	}
}

// backoffFn 第 n 次连续失败的等待时长：base*2^(n-1) + [0, d/2) 抖动，封顶 max。
func backoffFn(base, max time.Duration) func(int) time.Duration {
	return func(n int) time.Duration {
		d := base
		for i := 1; i < n && d < max; i++ {
			d *= 2
		}
		if d > max {
			d = max
		}
		return d + time.Duration(rand.Int63n(int64(d)/2+1))
	}
}

// inSession A 股交易时段判断（CST 无夏令时，FixedZone 免依赖系统 tzdata）：
// 周一至五 09:20–11:36、12:55–15:06，含开收盘前后缓冲。
func inSession(t time.Time) bool {
	cst := t.In(time.FixedZone("CST", 8*3600))
	switch cst.Weekday() {
	case time.Saturday, time.Sunday:
		return false
	}
	m := cst.Hour()*60 + cst.Minute()
	return (m >= 9*60+20 && m <= 11*60+36) || (m >= 12*60+55 && m <= 15*60+6)
}

// Name 源名。
func (p *Poller) Name() string { return "eastmoney" }

// Run 主循环：正常周期 interval，连续失败时按退避曲线等待；ctx 取消返回 nil。
func (p *Poller) Run(ctx context.Context) error {
	failures := 0
	for {
		wait := p.interval
		if failures > 0 {
			wait = p.backoff(failures)
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}

		if p.sessionGate && !inSession(time.Now()) {
			continue // 非交易时段：静默跳过，不计失败
		}

		if err := p.pollOnce(ctx); err != nil {
			failures++
			if p.log != nil {
				p.log.Warn("eastmoney poll failed", "failures", failures, "err", err)
			}
			continue
		}
		if failures > 0 && p.log != nil {
			p.log.Info("eastmoney poll recovered", "after_failures", failures)
		}
		failures = 0
	}
}

// pollOnce 分批拉取并发布；批间受 minGap 频控。
func (p *Poller) pollOnce(ctx context.Context) error {
	for start := 0; start < len(p.symbols); start += p.maxBatch {
		end := start + p.maxBatch
		if end > len(p.symbols) {
			end = len(p.symbols)
		}
		if start > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(p.minGap):
			}
		}
		ticks, _, err := p.client.Snapshot(ctx, p.symbols[start:end])
		if err != nil {
			return err
		}
		if _, err := p.ingest.Ingest(ctx, ticks); err != nil {
			return err
		}
	}
	return nil
}
