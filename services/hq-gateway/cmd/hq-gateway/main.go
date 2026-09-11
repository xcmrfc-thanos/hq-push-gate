// hq-gateway 进程入口：行情源接入、标准化、发布 tick_raw。只做组装，不写业务逻辑。
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/hqpush/gate/packages/configx"
	"github.com/hqpush/gate/packages/kafkax"
	"github.com/hqpush/gate/packages/obs"
	"github.com/hqpush/gate/services/hq-gateway/internal/application"
	"github.com/hqpush/gate/services/hq-gateway/internal/config"
	"github.com/hqpush/gate/services/hq-gateway/internal/repository"
	"github.com/hqpush/gate/services/hq-gateway/internal/source"
	"github.com/hqpush/gate/services/hq-gateway/internal/source/eastmoney"
	"github.com/hqpush/gate/services/hq-gateway/internal/source/sim"
	"github.com/hqpush/gate/services/hq-gateway/internal/transport"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := config.Load()
	configx.GuardProd([3]string{"HQG_INTERNAL_TOKEN", cfg.InternalToken, "dev-internal-token"})
	log := obs.NewLogger("hq-gateway")
	metrics := obs.NewMetrics()

	writer := kafkax.NewWriter(cfg.KafkaBrokers, log)
	defer writer.Close()

	ingest := application.NewIngestService(repository.NewKafkaTickPublisher(writer), log, metrics)
	apiMux := transport.NewMux(ingest, cfg.MaxBatchTicks, metrics, func() bool { return true })

	// 内置主动拉取源（docs/09 §5.1：源与模拟源可配置切换）；HTTP 被动接入始终可用
	var simCtrl transport.SimController
	if src := buildSource(cfg, log, ingest); src != nil {
		if t, ok := src.(*sim.Ticker); ok {
			simCtrl = t
		}
		// 源韧性（B21 联调修复）：Kafka 瞬断导致 Run 返回错误时按指数退避重启源，
		// 网关不退出（行情接入是主链路入口，瞬断退出=数据中断）；ctx 取消才真正停止。
		go func() {
			backoff := time.Second
			for {
				if err := src.Run(ctx); err != nil {
					if ctx.Err() != nil {
						return
					}
					log.Error("source exited, restarting", "source", src.Name(), "err", err, "backoff", backoff)
					select {
					case <-time.After(backoff):
					case <-ctx.Done():
						return
					}
					backoff = min(backoff*2, 30*time.Second)
					continue
				}
				return
			}
		}()
	}
	transport.RegisterBench(apiMux, simCtrl, cfg.InternalToken)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           obs.TraceMiddleware(apiMux), // trace_id 入口：X-Trace-Id 头 + context 贯穿
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	log.Info("hq-gateway started", "addr", cfg.HTTPAddr, "brokers", cfg.KafkaBrokers, "source", cfg.Source)

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server failed", "err", err)
			stop()
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	log.Info("hq-gateway stopped")
}

func buildSource(cfg config.Config, log *slog.Logger, ingest source.Ingest) source.Source {
	switch strings.ToLower(strings.TrimSpace(cfg.Source)) {
	case "eastmoney":
		return eastmoney.NewPoller(
			eastmoney.NewClient(), ingest, log,
			cfg.EastSymbols, cfg.EastInterval, cfg.EastMinGap, cfg.EastBackoff, cfg.EastBackoffMax,
			cfg.EastMaxBatch, cfg.EastSession,
		)
	case "sim":
		return sim.NewTicker(ingest, log, cfg.SimSymbols, cfg.SimRate, cfg.SimBatch)
	default: // off / 未知值：仅 HTTP 被动接入
		return nil
	}
}
