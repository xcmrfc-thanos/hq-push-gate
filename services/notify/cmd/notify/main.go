// notify 进程入口：消费 alert_event（内嵌 alert-inbox），投递 ws_push，重试与死信，超窗过期。
package main

import (
	"context"
	"net/http"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/proto"

	"github.com/hqpush/gate/packages/configx"
	"github.com/hqpush/gate/packages/contract"
	hqv1 "github.com/hqpush/gate/packages/contract/go/hq/v1"
	"github.com/hqpush/gate/packages/kafkax"
	"github.com/hqpush/gate/packages/obs"
	"github.com/hqpush/gate/packages/secretx"
	"github.com/hqpush/gate/services/notify/internal/application"
	"github.com/hqpush/gate/services/notify/internal/channel"
	"github.com/hqpush/gate/services/notify/internal/config"
	"github.com/hqpush/gate/services/notify/internal/repository"
	"github.com/hqpush/gate/services/notify/internal/transport"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := config.Load()
	configx.GuardProd(
		[3]string{"NOTIFY_INTERNAL_TOKEN", cfg.InternalToken, "dev-internal-token"},
		[3]string{"NOTIFY_HOOK_SECRET", cfg.HookSecret, ""}, // 回调验签密钥：prod 必须非空（docs/10 §9）
	)
	log := obs.NewLogger("notify")
	metrics := obs.NewMetrics()

	mysql, err := repository.NewMySQLStore(cfg.MySQLDSN)
	if err != nil {
		log.Error("open mysql failed", "err", err)
		return
	}
	defer mysql.Close()

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPass})
	defer rdb.Close()

	writer := kafkax.NewWriter(cfg.KafkaBrokers, log)
	defer writer.Close()

	inbox := application.NewInboxService(
		repository.NewAlertStore(mysql),
		repository.NewRedisRoute(rdb),
		repository.NewKafkaPublisher(writer),
		log, metrics, contract.AlertWindowSec*time.Second,
	)
	pull := application.NewPullService(mysql, contract.AlertWindowSec*time.Second)

	// 渠道通知（docs/10 §4；B16-B 渠道配置化）：DB 密文配置 30s 热加载，env 兜底；
	// 未配置的通道自动降级。主密钥未配 → 全 env 兜底（兼容现网）。
	ring, err := secretx.LoadKeys()
	if err != nil {
		log.Warn("master keys not configured; channel configs fall back to env", "err", err)
	} else {
		mysql.SetRing(ring) // api_developer_config app_secret v1: 密文解密（B18）
	}
	prov := channel.NewConfigProvider(mysql.DB(), ring, 30*time.Second)
	mailSender := channel.NewDynamicMailSender(prov, channel.MailConfig{
		Host: cfg.Mail.Host, Port: cfg.Mail.Port, TLSMode: cfg.Mail.TLSMode,
		Username: cfg.Mail.Username, Password: cfg.Mail.Password, Sender: cfg.Mail.Sender,
	})
	smsSender := channel.NewDynamicSmsSender(prov, channel.SmsConfig{
		Provider: cfg.Sms.Provider, AccessKeyID: cfg.Sms.AccessKeyID,
		AccessKeySecret: cfg.Sms.AccessKeySecret, SignName: cfg.Sms.SignName,
		TemplateCode: cfg.Sms.TemplateCode, Endpoint: cfg.Sms.Endpoint,
	})
	breaker := channel.NewCircuitBreaker(rdb, channel.Limits{
		MailDaily:     cfg.MailDailyLimit,
		SmsDaily:      cfg.SmsDailyLimit,
		SmsUserDaily:  cfg.SmsUserDailyLimit,
		MailUserDaily: cfg.MailUserDailyLimit,
	})
	dispatcher := application.NewDispatcher(mysql, mailSender, smsSender,
		channel.NewIMSender("dd"), channel.NewIMSender("feishu"), breaker,
		application.DispatchConfig{MajorPct: cfg.MajorPct}, log, metrics)
	inbox.SetDeliverHook(func(ctx context.Context, d *hqv1.AlertDelivery, ruleID int64, prefer string) {
		dispatcher.Dispatch(ctx, d, ruleID, prefer)
	})

	alertsHandled := metrics.Counter("notify_alert_events_total", "处理的 alert_event 数")
	retriesTotal := metrics.Counter("notify_retries_total", "notify_retry 重投次数")
	deadTotal := metrics.Counter("notify_dead_letters_total", "进入死信的消息数")
	cleanedTotal := metrics.Counter("notify_send_log_cleanup_total", "清理的 notify_send_log 条数")

	// alert_event 主消费循环
	go func() {
		reader := kafkax.NewReader(kafkax.ReaderConfig{
			Brokers: cfg.KafkaBrokers, Group: cfg.GroupID, Topic: contract.TopicAlertEvent,
		})
		defer reader.Close()
		for {
			m, err := reader.FetchMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Error("fetch alert_event failed", "err", err)
				continue
			}
			var evt hqv1.AlertEvent
			if err := proto.Unmarshal(m.Value, &evt); err != nil {
				log.Error("bad alert_event payload", "err", err)
				writer.DeadLetter(context.Background(), contract.TopicAlertEvent, m, err)
				_ = reader.CommitMessages(context.Background(), m)
				continue
			}
			msgCtx := kafkax.ContextWithTrace(ctx, m) // trace 贯穿：alert_event -> inbox -> ws_push
			if err := inbox.HandleAlertEvent(msgCtx, &evt); err != nil {
				// 写库/路由失败：转 notify_retry（指数退避由重试组控制），处理成功后再提交 offset
				obs.LoggerWithTrace(msgCtx, log).Error("handle alert event failed", "event_id", evt.EventId, "err", err)
				publishRetry(msgCtx, writer, contract.TopicAlertEvent, &m, &evt, retriesTotal)
				_ = reader.CommitMessages(context.Background(), m)
				continue
			}
			alertsHandled.WithLabelValues().Inc()
			_ = reader.CommitMessages(context.Background(), m)
		}
	}()

	// notify_retry 重试消费循环：超过 MAX_RETRY 进死信
	go func() {
		reader := kafkax.NewReader(kafkax.ReaderConfig{
			Brokers: cfg.KafkaBrokers, Group: cfg.RetryGroupID, Topic: contract.TopicNotifyRetry,
		})
		defer reader.Close()
		for {
			m, err := reader.FetchMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Error("fetch notify_retry failed", "err", err)
				continue
			}
			var evt hqv1.AlertEvent
			if err := proto.Unmarshal(m.Value, &evt); err != nil {
				writer.DeadLetter(context.Background(), contract.TopicNotifyRetry, m, err)
				_ = reader.CommitMessages(context.Background(), m)
				continue
			}
			msgCtx := kafkax.ContextWithTrace(ctx, m)
			if err := inbox.HandleAlertEvent(msgCtx, &evt); err != nil {
				count := retryCount(&m) + 1
				if count > cfg.MaxRetry {
					deadTotal.WithLabelValues().Inc()
					writer.DeadLetter(msgCtx, contract.TopicNotifyRetry, m, err)
					_ = reader.CommitMessages(context.Background(), m)
					continue
				}
				obs.LoggerWithTrace(msgCtx, log).Warn("retry alert event", "event_id", evt.EventId, "count", count, "err", err)
				publishRetry(msgCtx, writer, contract.TopicNotifyRetry, &m, &evt, retriesTotal)
				_ = reader.CommitMessages(context.Background(), m)
				continue
			}
			_ = reader.CommitMessages(context.Background(), m)
		}
	}()

	// 超窗 EXPIRED 扫描（保留窗口 72h，docs/07 §6）
	go func() {
		ticker := time.NewTicker(cfg.ExpireInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				n, err := mysql.ExpireStale(ctx, time.Now(), contract.AlertWindowSec*time.Second)
				if err != nil {
					log.Error("expire stale failed", "err", err)
					continue
				}
				if n > 0 {
					log.Info("expired stale deliveries", "count", n)
				}
			}
		}
	}()

	// 数据生命周期清理（docs/12 §1.1 #19）：notify_send_log 分批删除超过保留期的记录。
	go func() {
		ticker := time.NewTicker(cfg.CleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				before := time.Now().Add(-cfg.SendLogRetention)
				var total int64
				for {
					n, err := mysql.CleanupSendLogsBefore(ctx, before, cfg.CleanupBatchLimit)
					if err != nil {
						log.Error("cleanup send_log failed", "err", err)
						break
					}
					total += n
					if n < int64(cfg.CleanupBatchLimit) {
						break
					}
				}
				if total > 0 {
					cleanedTotal.WithLabelValues().Add(float64(total))
					log.Info("send_log cleanup done", "deleted", total, "before", before.Format(time.RFC3339))
				}
			}
		}
	}()

	// 内部 API：补拉/ACK + 服务商回调 + 渠道配置管理 + 健康检查 + 指标
	mux := transport.NewInternalMux(pull, cfg.InternalToken)
	transport.RegisterHooks(mux, mysql, repository.NewRuleBcastPublisher(writer), cfg.InternalToken, cfg.HookSecret, log)
	transport.RegisterChannelAdmin(mux, transport.ChannelAdminDeps{
		DB: mysql.DB(), Ring: ring, Prov: prov, Token: cfg.InternalToken,
	})
	mysqlStore := mysql
	metrics.Register(mux, func(ctx context.Context) bool { return mysqlStore.Ping(ctx) == nil })
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: obs.TraceMiddleware(mux), ReadHeaderTimeout: 5 * time.Second} // 内部 API 也接受调用方 X-Trace-Id
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("internal api failed", "err", err)
			stop()
		}
	}()
	log.Info("notify started", "addr", cfg.HTTPAddr)

	<-ctx.Done()
	_ = srv.Close()
	log.Info("notify stopped")
}

// publishRetry 转投重试 Topic，携带重试计数 header。
func publishRetry(ctx context.Context, w *kafkax.Writer, source string, m *kafka.Message, evt *hqv1.AlertEvent, counter *prometheus.CounterVec) {
	hdrs := append(m.Headers[:0:0], m.Headers...)
	hdrs = append(hdrs, kafka.Header{Key: "x-retry-count", Value: []byte(strconv.Itoa(retryCount(m) + 1))})
	hdrs = append(hdrs, kafka.Header{Key: "x-retry-at", Value: []byte(time.Now().UTC().Format(time.RFC3339))})
	val, err := proto.Marshal(evt)
	if err != nil {
		return
	}
	w.PublishRaw(ctx, contract.TopicNotifyRetry, string(m.Key), val, hdrs...)
}

// retryCount 从消息头读取重试计数。
func retryCount(m *kafka.Message) int {
	for _, h := range m.Headers {
		if h.Key == "x-retry-count" {
			n, _ := strconv.Atoi(string(h.Value))
			return n
		}
	}
	return 0
}
