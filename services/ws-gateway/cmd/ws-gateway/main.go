// ws-gateway 进程入口：WS 鉴权接入、订阅索引、槽位租约消费、ACK 转发与优雅 drain。
package main

import (
	"context"
	"net/http"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/hqpush/gate/packages/configx"
	"github.com/hqpush/gate/packages/contract/wsproto"
	"github.com/hqpush/gate/packages/obs"
	"github.com/hqpush/gate/services/ws-gateway/internal/application"
	"github.com/hqpush/gate/services/ws-gateway/internal/config"
	"github.com/hqpush/gate/services/ws-gateway/internal/repository"
	"github.com/hqpush/gate/services/ws-gateway/internal/transport"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := config.Load()
	configx.GuardProd([3]string{"WS_GATEWAY_JWT_SECRET", cfg.JWTSecret, "dev-jwt-secret"})
	log := obs.NewLogger("ws-gateway")
	metrics := obs.NewMetrics()

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPass})
	defer rdb.Close()

	hub := application.NewHub(application.NewTotalGauge(
		metrics.Gauge("ws_connections", "当前实例 WS 在线连接数").WithLabelValues()))
	quoteFrames := metrics.Counter("ws_quote_frames_total", "行情帧数")
	alertPushes := metrics.Counter("ws_alert_pushes_total", "告警推送数")
	klineFrames := metrics.Counter("ws_kline_frames_total", "K 线闭合推送数（B21）")
	staleSkipped := metrics.Counter("ws_stale_messages_total", "fencing 过滤的旧实例消息数")
	acksOk := metrics.Counter("ws_acks_forwarded_total", "成功转发的 ACK 数")
	acksFailed := metrics.Counter("ws_acks_failed_total", "转发失败的 ACK 数")

	subIndex := application.NewSubIndex(rdb, cfg.GatewayID, 3*cfg.LeaseRenew)
	acker := application.NewAckForwarder(cfg.NotifyURL, cfg.InternalToken, log).
		WithMetrics(func() { acksOk.WithLabelValues().Inc() }, func() { acksFailed.WithLabelValues().Inc() })
	offsets := repository.NewOffsetStore(rdb)

	// 槽位消费者按租约状态启停（扩缩容只调整租约，不创建/删除 Topic）
	var consumersMu sync.Mutex
	consumers := make(map[uint32]context.CancelFunc)
	startConsumer := func(slot uint32) {
		consumersMu.Lock()
		defer consumersMu.Unlock()
		if _, ok := consumers[slot]; ok {
			return
		}
		cctx, cancel := context.WithCancel(ctx)
		consumers[slot] = cancel
		c := application.NewSlotConsumer(cfg.KafkaBrokers, slot, cfg.GatewayID, offsets, hub, log).
			WithMetrics(func() { staleSkipped.WithLabelValues().Inc() },
				func(int) { quoteFrames.WithLabelValues().Inc() },
				func() { alertPushes.WithLabelValues().Inc() },
				func(int) { klineFrames.WithLabelValues().Inc() })
		go c.Run(cctx)
	}
	stopConsumer := func(slot uint32) {
		consumersMu.Lock()
		defer consumersMu.Unlock()
		if cancel, ok := consumers[slot]; ok {
			cancel()
			delete(consumers, slot)
		}
	}

	leaseMgr := application.NewLeaseManager(rdb, cfg.GatewayID, cfg.Slots, cfg.LeaseTTL, log)
	leaseMgr.OnAcquire = func(_ context.Context, slot uint32) { startConsumer(slot) }
	leaseMgr.OnLose = func(_ context.Context, slot uint32) { stopConsumer(slot) }
	go leaseMgr.Run(ctx, cfg.LeaseRenew)
	go acker.Run(ctx)

	// 心跳：conn:{userId} TTL 续期（心跳×3）
	go func() {
		ticker := time.NewTicker(cfg.LeaseRenew)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// B22 修复：symkeys 传在线订阅集合——symgw 索引 15s TTL 需心跳续期，
				// 此前传 nil 导致长连接扇出 15s 后断流（quote-push 路由查不到实例）。
				_ = subIndex.Touch(ctx, hub.UserIDs(), hub.Symbols())
			}
		}
	}()

	// 鉴权：用户 JWT + 开发者票据（tk_ 前缀，biz /open/v1/ws-ticket 签发，B18-3）
	auth := transport.NewTicketAuth(transport.NewJWTAuth(cfg.JWTSecret), repository.NewRedisTicketStore(rdb))
	userVip := repository.NewRedisUserVipStore(rdb)
	wsSrv := transport.NewWSServer(auth, subIndex, hub, acker, log, cfg.MaxSubs, cfg.FrameRate, userVip)
	// B22 套餐复核器（ADR-040）：按在线 user_id 周期复核权益（走 Redis user_vip 缓存，免打库）
	go application.NewEntitlementReconciler(userVip, hub, cfg.EntitleRecheck, log).Run(ctx)
	mux := http.NewServeMux()
	// trace_id 入口：Upgrade 请求读取/生成 X-Trace-Id（docs/08 §7.1.3）
	mux.Handle("GET /ws", obs.TraceMiddleware(wsSrv))
	metrics.Register(mux, func(context.Context) bool {
		return rdb.Ping(context.Background()).Err() == nil
	})
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("http server failed", "err", err)
			stop()
		}
	}()
	log.Info("ws-gateway started", "gateway_id", cfg.GatewayID, "slots", cfg.Slots, "addr", cfg.HTTPAddr)

	<-ctx.Done()
	// drain：通知客户端重连，等待优雅退出
	hub.DrainAll(func(c *application.Conn) {
		select {
		case c.SysCh <- wsproto.SysData{Code: "drain"}:
		default:
		}
	})
	time.Sleep(cfg.DrainGrace)
	_ = srv.Close()
	log.Info("ws-gateway stopped")
}
