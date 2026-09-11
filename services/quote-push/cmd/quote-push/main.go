// quote-push 进程入口：消费 tick_raw，合并快照并扇出到 ws_push 固定槽位分区。
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/proto"

	"github.com/hqpush/gate/packages/contract"
	hqv1 "github.com/hqpush/gate/packages/contract/go/hq/v1"
	"github.com/hqpush/gate/packages/kafkax"
	"github.com/hqpush/gate/packages/obs"
	"github.com/hqpush/gate/services/quote-push/internal/application"
	"github.com/hqpush/gate/services/quote-push/internal/config"
	"github.com/hqpush/gate/services/quote-push/internal/repository"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := config.Load()
	log := obs.NewLogger("quote-push")
	metrics := obs.NewMetrics()

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPass})
	defer rdb.Close()
	store := repository.NewRedisStore(rdb)

	writer := kafkax.NewWriter(cfg.KafkaBrokers, log)
	defer writer.Close()

	merger := application.NewMerger()
	router := application.NewRouter(store, 1*time.Second)

	consumed := metrics.Counter("quote_push_ticks_consumed_total", "消费的 tick 数")
	published := metrics.Counter("quote_push_ws_push_messages_total", "写入 ws_push 的消息数", "slot")
	snapErrs := metrics.Counter("quote_push_snapshot_errors_total", "Redis 快照写失败次数")
	routeErrs := metrics.Counter("quote_push_route_errors_total", "路由解析失败次数")
	klineConsumed := metrics.Counter("quote_push_klines_consumed_total", "消费的闭合 K 线数")
	klinePublished := metrics.Counter("quote_push_kline_ws_push_total", "K 线写入 ws_push 条数", "slot")

	// 行情合并窗口 flush
	flushDone := make(chan struct{})
	go func() {
		defer close(flushDone)
		ticker := time.NewTicker(cfg.MergeWindow)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				flush(ctx, merger, router, store, writer, published, snapErrs, routeErrs, log)
			}
		}
	}()

	// tick_raw 消费循环（组消费、offset 批量提交）：
	// 消费侧只记录最高 offset（提交 m 即按分区推进到 m.Offset+1，覆盖其前全部消息），
	// 独立 committer 每周期提交一次 + 退出冲刷。逐条同步提交实测回放积压仅 ~200/s
	// （docs/12 U2 性能项）；tick 丢旧保新允许重复，崩溃回放至多一个提交窗口。
	reader := kafkax.NewReader(kafkax.ReaderConfig{
		Brokers: cfg.KafkaBrokers, Group: cfg.GroupID, Topic: contract.TopicTickRaw,
	})
	defer reader.Close()
	pending := &offsetPending{}
	consumeDone := make(chan struct{})
	go func() {
		defer close(consumeDone)
		defer func() { // 退出冲刷：最后一窗的 offset 先提交再关 reader（defer 声明在 reader.Close 之后，先执行）
			if m := pending.Take(); m != nil {
				commitWithTimeout(reader, *m, log)
			}
		}()
		for {
			m, err := reader.FetchMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Error("fetch tick failed", "err", err)
				continue
			}
			var t hqv1.Tick
			if err := proto.Unmarshal(m.Value, &t); err != nil {
				log.Error("bad tick payload", "err", err)
				// 解码失败属于不可恢复消息，跳过不进死信（tick 允许丢旧保新）；
				// offset 交给批量提交推进
				pending.Mark(m)
				continue
			}
			// trace 提取：x-trace-id 消息头 -> merger 随 tick 保留，flush 时尽力传播
			msgCtx := kafkax.ContextWithTrace(ctx, m)
			merger.Add(&t, obs.TraceFromCtx(msgCtx))
			consumed.WithLabelValues().Inc()
			pending.Mark(m)
		}
	}()

	// offset 批量提交循环：每周期提交一次最高 offset（kafka-go Reader 并发安全）
	commitDone := make(chan struct{})
	go func() {
		defer close(commitDone)
		ticker := time.NewTicker(cfg.CommitInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if m := pending.Take(); m != nil {
					commitWithTimeout(reader, *m, log)
				}
			}
		}
	}()

	// snapshot_kline 消费循环（B21）：周期闭合 K 线 → 路由扇出 ws_push（Kline payload）
	// + Redis kline:cur 当前 bar（订阅即回补的"当前值"）。低频（每 symbol 每周期 1 条/分钟）。
	klineDone := make(chan struct{})
	go func() {
		defer close(klineDone)
		reader := kafkax.NewReader(kafkax.ReaderConfig{
			Brokers: cfg.KafkaBrokers, Group: cfg.GroupID + "-kline", Topic: contract.TopicSnapshotKline,
		})
		defer reader.Close()
		for {
			m, err := reader.FetchMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Error("fetch kline failed", "err", err)
				continue
			}
			var k hqv1.Kline
			if err := proto.Unmarshal(m.Value, &k); err != nil {
				log.Error("bad kline payload", "err", err)
				commitWithTimeout(reader, m, log)
				continue
			}
			klineConsumed.WithLabelValues().Inc()
			symKey := k.Market.String() + ":" + k.Symbol
			_ = store.WriteKlineCur(ctx, k.Market.String(), k.Symbol, int(k.PeriodMin), m.Value)
			targets, err := router.Resolve(ctx, []string{symKey})
			if err != nil {
				routeErrs.WithLabelValues().Inc()
				log.Error("resolve kline route failed", "err", err)
				commitWithTimeout(reader, m, log)
				continue
			}
			for _, tgt := range targets[symKey] {
				key := strconv.FormatUint(uint64(tgt.GatewaySlot), 10)
				wsMsg := &hqv1.WsPushMsg{
					GatewayId:   tgt.GatewayID,
					GatewaySlot: tgt.GatewaySlot,
					Payload:     &hqv1.WsPushMsg_Kline{Kline: &k},
				}
				if err := writer.PublishProto(ctx, contract.TopicWsPush, key, wsMsg); err != nil {
					obs.LoggerWithTrace(ctx, log).Error("publish kline ws_push failed", "slot", tgt.GatewaySlot, "err", err)
					continue
				}
				published.WithLabelValues(key).Inc()
				klinePublished.WithLabelValues().Inc()
			}
			commitWithTimeout(reader, m, log)
		}
	}()

	// 管理端口：健康检查与指标
	adminMux := http.NewServeMux()
	metrics.Register(adminMux, func(context.Context) bool {
		return rdb.Ping(context.Background()).Err() == nil
	})
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: adminMux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("admin server failed", "err", err)
			stop()
		}
	}()
	log.Info("quote-push started", "merge_window", cfg.MergeWindow)

	<-ctx.Done()
	_ = srv.Close()
	select {
	case <-consumeDone:
	case <-time.After(5 * time.Second):
	}
	select {
	case <-commitDone:
	case <-time.After(time.Second):
	}
	select {
	case <-klineDone:
	case <-time.After(2 * time.Second):
	}
	<-flushDone
	log.Info("quote-push stopped")
}

// commitWithTimeout 提交 offset：带上限的独立 context，防协调器卡死阻塞消费循环
// （tick 允许重复/丢失——提交失败仅记日志，消费继续；实机回归发现的停滞根因）。
func commitWithTimeout(reader *kafka.Reader, m kafka.Message, log *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := reader.CommitMessages(ctx, m); err != nil {
		log.Warn("commit tick offset failed (continue)", "offset", m.Offset, "err", err)
	}
}

// offsetPending 批量提交缓冲：仅保留最高 offset 的消息（Kafka offset 提交按分区推进，
// 提交 m 即确认其前全部消息），由周期 committer 取走提交，退出前冲刷兜底。
type offsetPending struct {
	mu   sync.Mutex
	last *kafka.Message
}

func (p *offsetPending) Mark(m kafka.Message) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.last == nil || m.Offset > p.last.Offset {
		p.last = &m
	}
}

func (p *offsetPending) Take() *kafka.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	m := p.last
	p.last = nil
	return m
}

// flush 合并窗口到期：写 Redis 快照并按槽位扇出 ws_push。
// trace 尽力传播：同一批 tick 可能来自多条链路，取批内首条 tick 的 trace_id。
func flush(
	ctx context.Context,
	merger *application.Merger,
	router *application.Router,
	store *repository.RedisStore,
	writer *kafkax.Writer,
	published *prometheus.CounterVec,
	snapErrs, routeErrs *prometheus.CounterVec,
	log *slog.Logger,
) {
	items := merger.Flush()
	if len(items) == 0 {
		return
	}
	ticks := make([]*hqv1.Tick, len(items))
	traceByTick := make(map[*hqv1.Tick]string, len(items))
	for i, it := range items {
		ticks[i] = it.Tick()
		traceByTick[it.Tick()] = it.TraceID()
	}
	if err := store.WriteSnapshots(ctx, ticks); err != nil {
		snapErrs.WithLabelValues().Inc()
		log.Error("write snapshots failed", "count", len(ticks), "err", err)
	}
	symKeys := make([]string, 0, len(ticks))
	seen := make(map[string]struct{}, len(ticks))
	for _, t := range ticks {
		k := t.Market.String() + ":" + t.Symbol
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			symKeys = append(symKeys, k)
		}
	}
	targetsBySym, err := router.Resolve(ctx, symKeys)
	if err != nil {
		routeErrs.WithLabelValues().Inc()
		log.Error("resolve route failed", "err", err)
		return
	}
	batches := router.Fanout(ticks, targetsBySym)
	for tgt, bt := range batches {
		key := strconv.FormatUint(uint64(tgt.GatewaySlot), 10)
		// 批次 trace：首条 tick 的 trace_id（合并窗口内多链路时为尽力传播）
		batchCtx := ctx
		if len(bt) > 0 {
			if tr := traceByTick[bt[0]]; tr != "" {
				batchCtx = obs.WithTrace(ctx, tr)
			}
		}
		wsMsg := &hqv1.WsPushMsg{
			GatewayId:   tgt.GatewayID,
			GatewaySlot: tgt.GatewaySlot,
			Payload: &hqv1.WsPushMsg_Quotes{Quotes: &hqv1.QuoteBatch{Ticks: bt}},
		}
		if err := writer.PublishProto(batchCtx, contract.TopicWsPush, key, wsMsg); err != nil {
			obs.LoggerWithTrace(batchCtx, log).Error("publish ws_push failed", "slot", tgt.GatewaySlot, "err", err)
			continue
		}
		published.WithLabelValues(key).Inc()
	}
}
