// Package sim 内置模拟源：几何随机游走生成 A 股 tick，供收盘后/无外网环境联调全链路。
// 口径对齐 bench/tick-source（外部推送型模拟源，压测用）；本包为进程内源，配置切换即用，
// 支持经 /internal/bench/tick-source 运行时调压。
package sim

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"sync/atomic"
	"time"

	"github.com/hqpush/gate/services/hq-gateway/internal/domain"
	"github.com/hqpush/gate/services/hq-gateway/internal/source"
)

type Ticker struct {
	ingest source.Ingest
	log    *slog.Logger
	count  int          // 模拟标的数量（600000 起）
	rate   atomic.Int64 // 每秒 tick 批次数（float64 位模式；可运行时调压）
	batch  int          // 每批 tick 数
}

// NewTicker 构造模拟源。
func NewTicker(ingest source.Ingest, log *slog.Logger, count int, rate float64, batch int) *Ticker {
	t := &Ticker{ingest: ingest, log: log, count: count, batch: batch}
	t.UpdateRate(rate)
	return t
}

func (t *Ticker) Name() string { return "sim" }

// UpdateRate 运行时调压（/internal/bench/tick-source 调用）。
func (t *Ticker) UpdateRate(rate float64) { t.rate.Store(int64(math.Float64bits(rate))) }

// Rate 当前速率。
func (t *Ticker) Rate() float64 { return math.Float64frombits(uint64(t.rate.Load())) }

type symState struct {
	symbol string
	price  float64
	pre    float64
	vol    float64
}

// Run 以 100ms 步进节拍生成：每步 emit = ceil(rate/10) 批，速率变更即时生效。
func (t *Ticker) Run(ctx context.Context) error {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	syms := make([]*symState, t.count)
	for i := range syms {
		pre := 10 + rng.Float64()*90
		syms[i] = &symState{
			symbol: fmt.Sprintf("%06d", 600000+i),
			price:  pre,
			pre:    pre,
			vol:    rng.Float64() * 1e5,
		}
	}

	const step = 100 * time.Millisecond
	tk := time.NewTicker(step)
	defer tk.Stop()

	batch := make([]*domain.Tick, 0, t.batch)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if _, err := t.ingest.Ingest(ctx, batch); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return flush()
		case <-tk.C:
		}
		emit := int(t.Rate() * float64(step) / float64(time.Second))
		if emit < 1 {
			emit = 1
		}
		now := time.Now().UnixMilli()
		for i := 0; i < emit*t.batch; i++ {
			s := syms[rng.Intn(len(syms))]
			// ±0.5% 内随机游走，涨跌幅相对昨收可控
			s.price = s.price * (1 + (rng.Float64()-0.5)/100)
			s.vol += rng.Float64() * 100
			batch = append(batch, &domain.Tick{
				Market:      domain.MarketAShare,
				Symbol:      s.symbol,
				Exchange:    "SSE",
				TimestampMS: now,
				LastPrice:   round2(s.price),
				Open:        round2(s.pre),
				High:        round2(s.price * 1.005),
				Low:         round2(s.price * 0.995),
				PreClose:    round2(s.pre),
				Volume:      s.vol,
				Amount:      s.vol * s.price,
			})
			if len(batch) >= cap(batch) {
				if err := flush(); err != nil {
					return err
				}
			}
		}
		if err := flush(); err != nil {
			return err
		}
	}
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }
