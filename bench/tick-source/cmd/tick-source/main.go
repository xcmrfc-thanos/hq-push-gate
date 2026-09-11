// tick-source 模拟源：按目标速率生成标准化 tick 并批量推送 hq-gateway（docs/04 §6 /admin/bench 模拟源）。
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hqpush/gate/bench/tick-source/internal/config"
	"github.com/hqpush/gate/packages/obs"
)

type tick struct {
	Market      string  `json:"market"`
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

func main() {
	cfg := config.Load()
	log := obs.NewLogger("tick-source")

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	type symState struct {
		market, symbol string
		price, pre     float64
		vol            float64
	}
	syms := make([]*symState, 0, len(cfg.Markets)*cfg.Symbols)
	for _, m := range cfg.Markets {
		for i := 0; i < cfg.Symbols; i++ {
			pre := 10 + rng.Float64()*90
			syms = append(syms, &symState{
				market: m, symbol: fmt.Sprintf("%06d", 600000+i),
				price: pre, pre: pre, vol: rng.Float64() * 1e6,
			})
		}
	}

	client := &http.Client{Timeout: 3 * time.Second}
	url := cfg.GatewayURL + "/ingest/ticks"

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	interval := time.Duration(float64(time.Second) / cfg.Rate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	sent := 0
	start := time.Now()
	batch := make([]tick, 0, cfg.BatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		body, _ := json.Marshal(batch)
		resp, err := client.Post(url, "application/json", bytes.NewReader(body))
		if err != nil {
			log.Error("post ticks failed", "err", err)
		} else {
			_ = resp.Body.Close()
			sent += len(batch)
		}
		batch = batch[:0]
	}

	log.Info("tick-source started", "rate", cfg.Rate, "symbols", len(syms), "url", url)
	for {
		select {
		case <-stop:
			flush()
			log.Info("tick-source stopped", "sent", sent, "elapsed", time.Since(start).Round(time.Second))
			return
		case <-ticker.C:
			s := syms[rng.Intn(len(syms))]
			drift := (rng.Float64() - 0.5) * s.pre * 0.002
			s.price += drift
			if s.price < 0.01 {
				s.price = 0.01
			}
			s.vol += rng.Float64() * 1000
			batch = append(batch, tick{
				Market: s.market, Symbol: s.symbol, Exchange: "SIM",
				TimestampMS: time.Now().UnixMilli(),
				LastPrice:   s.price, Open: s.pre, High: max(s.price, s.pre),
				Low: min(s.price, s.pre), PreClose: s.pre,
				Volume: s.vol, Amount: s.vol * s.price,
			})
			if len(batch) >= cfg.BatchSize {
				flush()
			}
			if cfg.Duration > 0 && time.Since(start) > time.Duration(cfg.Duration)*time.Second {
				flush()
				log.Info("tick-source finished", "sent", sent)
				return
			}
		}
	}
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
