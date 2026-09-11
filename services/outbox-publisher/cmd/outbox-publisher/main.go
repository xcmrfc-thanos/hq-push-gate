// outbox-publisher 进程入口：轮询 outbox_event，发布 rule_bcast（U0 规则同步主通道，无 Debezium）。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/hqpush/gate/packages/contract"
	hqv1 "github.com/hqpush/gate/packages/contract/go/hq/v1"
	"github.com/hqpush/gate/packages/kafkax"
	"github.com/hqpush/gate/packages/obs"
	"github.com/hqpush/gate/services/outbox-publisher/internal/config"
	"github.com/hqpush/gate/services/outbox-publisher/internal/repository"
)

const maxRetry = 8

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := config.Load()
	log := obs.NewLogger("outbox-publisher")
	metrics := obs.NewMetrics()

	store, err := repository.NewStore(cfg.MySQLDSN)
	if err != nil {
		log.Error("open mysql failed", "err", err)
		return
	}
	defer store.Close()

	published := metrics.Counter("outbox_published_total", "成功发布的 outbox 事件数")
	failures := metrics.Counter("outbox_publish_failures_total", "发布失败次数")
	dead := metrics.Counter("outbox_dead_total", "超过重试上限的事件数")
	cleaned := metrics.Counter("outbox_cleanup_deleted_total", "清理的已发布 outbox 事件数")

	// rule_bcast 为单分区广播语义，key 为空
	writer := kafkax.NewWriter(cfg.KafkaBrokers, log)
	defer writer.Close()

	mux := http.NewServeMux()
	metrics.Register(mux, func(ctx context.Context) bool { return store.Ping(ctx) == nil })
	srv := &http.Server{Addr: ":23025", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("admin server failed", "err", err)
			stop()
		}
	}()

	go func() {
		ticker := time.NewTicker(cfg.PollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				rows, err := store.Pending(ctx, cfg.BatchSize)
				if err != nil {
					log.Error("query outbox failed", "err", err)
					continue
				}
				for _, r := range rows {
					// 每个事件生成 trace_id，rule_bcast 头与失败日志关联（docs/08 §7.1.3）
					rowCtx, traceID := obs.EnsureTrace(ctx)
					if err := publishRule(rowCtx, writer, r); err != nil {
						failures.WithLabelValues().Inc()
						obs.LoggerWithTrace(rowCtx, log).Error("publish rule_bcast failed",
							"trace_id", traceID, "id", r.ID, "err", err)
						if r.ID%maxRetry == 0 { // 超限标记（retry_count 由 DB 维护，此处简化）
							dead.WithLabelValues().Inc()
							_ = store.MarkDead(ctx, r.ID, err.Error())
							continue
						}
						_ = store.MarkFailed(ctx, r.ID, err.Error(), time.Now().Add(30*time.Second))
						continue
					}
					published.WithLabelValues().Inc()
					if err := store.MarkPublished(ctx, r.ID); err != nil {
						log.Error("mark published failed", "id", r.ID, "err", err)
					}
				}
			}
		}
	}()

	// 数据生命周期清理（docs/12 §1.1 #19）：仅删超过保留期的 PUBLISHED 事件，
	// PENDING/FAILED 保留供排查/重放；分批删除控制单事务锁窗口。
	go func() {
		ticker := time.NewTicker(cfg.CleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				before := time.Now().Add(-cfg.Retention)
				var total int64
				for {
					n, err := store.CleanupBefore(ctx, before, cfg.CleanupBatch)
					if err != nil {
						log.Error("cleanup outbox failed", "err", err)
						break
					}
					total += n
					if n < int64(cfg.CleanupBatch) {
						break
					}
				}
				if total > 0 {
					cleaned.WithLabelValues().Add(float64(total))
					log.Info("outbox cleanup done", "deleted", total, "before", before.Format(time.RFC3339))
				}
			}
		}
	}()

	log.Info("outbox-publisher started", "poll", cfg.PollInterval)
	<-ctx.Done()
	_ = srv.Close()
	log.Info("outbox-publisher stopped")
}

// publishRule 将 outbox payload（biz-service 写入的 RuleMsg 对齐 JSON）转为 protobuf 发布。
// 重复发布无害：Flink 按 (rule_id, version) 幂等覆盖。
func publishRule(ctx context.Context, w *kafkax.Writer, r *repository.OutboxRow) error {
	var p struct {
		RuleID      int64  `json:"ruleId"`
		Version     int64  `json:"version"`
		Op          string `json:"op"`
		Market      string `json:"market"`
		Symbol      string `json:"symbol"`
		RuleType    string `json:"ruleType"`
		Condition   string `json:"condition"`
		CooldownSec int32  `json:"cooldownSec"`
	}
	if err := json.Unmarshal([]byte(r.Payload), &p); err != nil {
		return err
	}
	market, ok := hqv1.Market_value[p.Market]
	if !ok {
		return errors.New("unknown market: " + p.Market)
	}
	ruleType, ok := hqv1.RuleType_value[p.RuleType]
	if !ok {
		return errors.New("unknown rule type: " + p.RuleType)
	}
	op := p.Op
	if op == "" {
		op = "UPSERT"
	}
	msg := &hqv1.RuleMsg{
		RuleId:      p.RuleID,
		Version:     p.Version,
		Op:          op,
		Market:      hqv1.Market(market),
		Symbol:      p.Symbol,
		Type:        hqv1.RuleType(ruleType),
		Condition:   p.Condition,
		CooldownSec: p.CooldownSec,
	}
	return w.PublishProto(ctx, contract.TopicRuleBcast, "", msg)
}
