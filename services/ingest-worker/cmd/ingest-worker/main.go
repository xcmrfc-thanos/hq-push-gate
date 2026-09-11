// ingest-worker 进程入口：tick_raw → ClickHouse 明细幂等落库；alert_event → Doris 归档（可关）。
package main

import (
	"context"
	"log/slog"
	"os/signal"
	"syscall"
	"time"

	"github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/proto"

	"github.com/hqpush/gate/packages/contract"
	hqv1 "github.com/hqpush/gate/packages/contract/go/hq/v1"
	"github.com/hqpush/gate/packages/kafkax"
	"github.com/hqpush/gate/packages/obs"
	"github.com/hqpush/gate/services/ingest-worker/internal/config"
	"github.com/hqpush/gate/services/ingest-worker/internal/repository"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := config.Load()
	log := obs.NewLogger("ingest-worker")
	metrics := obs.NewMetrics()

	ticksIn := metrics.Counter("ingest_ticks_consumed_total", "消费 tick 数")
	ticksWritten := metrics.Counter("ingest_ticks_written_total", "写入 ClickHouse 行数")
	writeErrs := metrics.Counter("ingest_write_errors_total", "写入失败次数")

	var ck *repository.ClickHouseStore
	if cfg.ClickHouseDSN != "" {
		var err error
		ck, err = repository.NewClickHouseStore(ctx, cfg.ClickHouseDSN)
		if err != nil {
			log.Error("clickhouse unavailable, running without CK sink", "err", err)
			ck = nil
		} else {
			defer ck.Close()
		}
	}

	// alert_event -> Doris 归档（U0 默认关闭，docs/08 §3.0）
	var doris *repository.DorisStore
	if cfg.DorisDSN != "" {
		var err error
		doris, err = repository.NewDorisStore(ctx, cfg.DorisDSN)
		if err != nil {
			log.Error("doris unavailable, archive disabled", "err", err)
		} else {
			defer doris.Close()
		}
	}
	if doris != nil {
		go runAlertArchive(ctx, cfg, doris, log)
	}
	if ck != nil {
		go runKlineArchive(ctx, cfg, ck, log)
	}

	// tick 批量缓冲
	batch := make([]repository.TickRow, 0, cfg.BatchSize)
	var batchTrace string // 批首消息的 trace_id，写入失败日志用于链路关联
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if ck == nil {
			batch = batch[:0]
			return
		}
		version := uint64(time.Now().UnixMicro())
		if err := ck.InsertTicks(ctx, batch, version); err != nil {
			writeErrs.WithLabelValues().Inc()
			obs.LoggerWithTrace(obs.WithTrace(ctx, batchTrace), log).
				Error("insert ticks failed", "count", len(batch), "err", err)
			return // 失败保留 offset 重读（Kafka 消费重复由 CK 幂等键去重）
		}
		ticksWritten.WithLabelValues().Add(float64(len(batch)))
		batch = batch[:0]
		batchTrace = ""
	}

	reader := kafkax.NewReader(kafkax.ReaderConfig{
		Brokers: cfg.KafkaBrokers, Group: cfg.GroupID, Topic: contract.TopicTickRaw,
	})
	defer reader.Close()

	// 定时 flush
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(time.Duration(cfg.FlushInterval) * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				close(done)
				return
			case <-ticker.C:
				flush()
			}
		}
	}()

	log.Info("ingest-worker started", "ck", ck != nil, "doris", cfg.DorisDSN != "")
	pending := make([]kafka.Message, 0, cfg.BatchSize)
	for {
		m, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			log.Error("fetch tick_raw failed", "err", err)
			continue
		}
		var t hqv1.Tick
		if err := proto.Unmarshal(m.Value, &t); err != nil {
			log.Error("bad tick payload", "err", err)
			_ = reader.CommitMessages(context.Background(), m)
			continue
		}
		if len(batch) == 0 {
			batchTrace = obs.TraceFromCtx(kafkax.ContextWithTrace(ctx, m))
		}
		batch = append(batch, repository.TickRow{
			Market: t.Market.String(), Symbol: t.Symbol, TSMS: t.TimestampMs,
			LastPrice: t.LastPrice, Open: t.Open, High: t.High, Low: t.Low,
			PreClose: t.PreClose, Volume: t.Volume, Amount: t.Amount,
		})
		pending = append(pending, m)
		ticksIn.WithLabelValues().Inc()
		if len(batch) >= cfg.BatchSize {
			flush()
			commitAll(reader, &pending)
		}
	}
	// 退出前冲刷
	flush()
	commitAll(reader, &pending)
	<-done
	log.Info("ingest-worker stopped")
}

// commitAll 仅在批次成功写入后提交 offset（Kafka 消费重复由 CK 幂等键去重）。
func commitAll(reader *kafka.Reader, pending *[]kafka.Message) {
	if len(*pending) == 0 {
		return
	}
	msgs := *pending
	_ = reader.CommitMessages(context.Background(), msgs...)
	*pending = (*pending)[:0]
}

// runKlineArchive 消费 snapshot_kline 归档 ClickHouse kline_local（独立消费组，
// event_id=market:symbol:period:begin_ts 幂等，docs/04 §2/§4）。
func runKlineArchive(ctx context.Context, cfg config.Config, ck *repository.ClickHouseStore, log *slog.Logger) {
	reader := kafkax.NewReader(kafkax.ReaderConfig{
		Brokers: cfg.KafkaBrokers, Group: cfg.GroupID + "-kline", Topic: contract.TopicSnapshotKline,
	})
	defer reader.Close()
	batch := make([]repository.KlineRow, 0, cfg.BatchSize)
	var last kafka.Message
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := ck.InsertKlines(ctx, batch, uint64(time.Now().UnixMicro())); err != nil {
			obs.LoggerWithTrace(kafkax.ContextWithTrace(ctx, last), log).
				Error("archive snapshot_kline failed", "count", len(batch), "err", err)
			return // 不提交 offset，由重放兜底（CK 幂等键去重）
		}
		batch = batch[:0]
		_ = reader.CommitMessages(context.Background(), last)
	}
	ticker := time.NewTicker(time.Duration(cfg.FlushInterval) * time.Millisecond)
	defer ticker.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				flush()
			}
		}
	}()
	for {
		m, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		var k hqv1.Kline
		if err := proto.Unmarshal(m.Value, &k); err != nil {
			log.Error("bad snapshot_kline payload", "err", err)
			_ = reader.CommitMessages(context.Background(), m)
			continue
		}
		batch = append(batch, repository.KlineRow{
			Market: k.Market.String(), Symbol: k.Symbol, PeriodMin: uint16(k.PeriodMin),
			BeginTS: k.BeginTsMs, Open: k.Open, High: k.High, Low: k.Low, Close: k.Close,
			Volume: k.Volume, Amount: k.Amount,
		})
		last = m
		if len(batch) >= cfg.BatchSize {
			flush()
		}
	}
}

// runAlertArchive 消费 alert_event 归档 Doris（独立消费组，幂等键 event_id）。
func runAlertArchive(ctx context.Context, cfg config.Config, doris *repository.DorisStore, log *slog.Logger) {
	reader := kafkax.NewReader(kafkax.ReaderConfig{
		Brokers: cfg.KafkaBrokers, Group: cfg.GroupID + "-archive", Topic: contract.TopicAlertEvent,
	})
	defer reader.Close()
	batch := make([]repository.AlertEventRow, 0, cfg.BatchSize)
	var last kafka.Message
	for {
		m, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		var evt hqv1.AlertEvent
		if err := proto.Unmarshal(m.Value, &evt); err != nil {
			log.Error("bad alert_event payload", "err", err)
			_ = reader.CommitMessages(context.Background(), m)
			continue
		}
		batch = append(batch, repository.AlertEventRow{
			EventID: evt.EventId, RuleID: evt.RuleId, Version: evt.Version,
			Market: evt.Market.String(), Symbol: evt.Symbol, Title: evt.Title,
			DetailJSON: evt.DetailJson, TriggerTS: evt.TriggerTs,
		})
		last = m
		if len(batch) >= cfg.BatchSize {
			if err := doris.InsertAlertEvents(ctx, batch); err != nil {
				// trace 贯穿：归档失败日志携带告警链路 trace_id
				obs.LoggerWithTrace(kafkax.ContextWithTrace(ctx, last), log).
					Error("archive alert_event failed", "count", len(batch), "err", err)
				continue // 不提交 offset，由重放兜底
			}
			batch = batch[:0]
			_ = reader.CommitMessages(context.Background(), last)
		}
	}
}
