// Package transport WebSocket 接入层：Upgrade 复核 JWT、订阅/ACK 协议、读写循环。
package transport

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"

	"github.com/hqpush/gate/packages/contract/wsproto"
	"github.com/hqpush/gate/services/ws-gateway/internal/application"
	"github.com/hqpush/gate/services/ws-gateway/internal/domain"
	"github.com/hqpush/gate/services/ws-gateway/internal/repository"
)

// Authenticator Upgrade 阶段业务授权（docs/07 §5.3：ws-gateway 二次校验短期 token）。
type Authenticator interface {
	VerifyToken(token string) (userID int64, err error)
}

// TicketStore 开发者 WS 票据存取（Redis 实现：open:ws:{ticket} → uid，60s TTL，取后即焚）。
type TicketStore interface {
	Take(ctx context.Context, ticket string) (uid int64, ok bool)
}

// TicketAuth 开发者 WS 票据分支（docs/11 §5.2，B18-3）：token 形如 tk_<hex> 时走票据，
// 否则回落 inner（用户 JWT）。票据由 biz-service /open/v1/ws-ticket 签发。
type TicketAuth struct {
	inner Authenticator
	store TicketStore
}

func NewTicketAuth(inner Authenticator, store TicketStore) *TicketAuth {
	return &TicketAuth{inner: inner, store: store}
}

func (a *TicketAuth) VerifyToken(token string) (int64, error) {
	if len(token) > 3 && token[:3] == "tk_" {
		if a.store == nil {
			return 0, errors.New("ticket auth unavailable")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		uid, ok := a.store.Take(ctx, token)
		if !ok {
			return 0, errors.New("invalid or expired ticket")
		}
		return uid, nil
	}
	return a.inner.VerifyToken(token)
}

// JWTAuth HS256 实现，claims: uid/exp/typ（docs/11 §7.1 契约：typ 必须 = access，
// refresh token 不得用于 WS；uid 数值 claim 由 biz-service TokenService 统一签发）。
type JWTAuth struct{ secret []byte }

func NewJWTAuth(secret string) *JWTAuth { return &JWTAuth{secret: []byte(secret)} }

func (a *JWTAuth) VerifyToken(token string) (int64, error) {
	t, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return a.secret, nil
	})
	if err != nil || !t.Valid {
		return 0, errors.New("invalid token")
	}
	claims, ok := t.Claims.(jwt.MapClaims)
	if !ok {
		return 0, errors.New("invalid claims")
	}
	if typ, _ := claims["typ"].(string); typ != "access" {
		return 0, errors.New("token type must be access")
	}
	uid, ok := claims["uid"].(float64)
	if !ok || uid <= 0 {
		return 0, errors.New("missing uid")
	}
	return int64(uid), nil
}

// SubscriptionOps 订阅变更（application.SubIndex 适配）。
type SubscriptionOps interface {
	Subscribe(ctx context.Context, symKeys ...string) error
	Unsubscribe(ctx context.Context, symKeys ...string) error
}

// Acker ACK 转发队列。
type Acker interface {
	AckAsync(deliveryID string)
}

// WSServer WebSocket 服务。
type WSServer struct {
	upgrader  websocket.Upgrader
	auth      Authenticator
	subs      SubscriptionOps
	hub       *application.Hub
	acker     Acker
	log       *slog.Logger
	maxSubs   int
	frameRate int
	userVip   repository.UserVipStore // B22 权益读取（建连初始化；复核器独立于本结构）
}

func NewWSServer(auth Authenticator, subs SubscriptionOps, hub *application.Hub, acker Acker, log *slog.Logger, maxSubs, frameRate int, userVip repository.UserVipStore) *WSServer {
	return &WSServer{
		upgrader: websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096},
		auth:     auth, subs: subs, hub: hub, acker: acker, log: log,
		maxSubs: maxSubs, frameRate: frameRate, userVip: userVip,
	}
}

// ServeHTTP 处理 /ws 升级。
func (s *WSServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 1) 业务授权：Upgrade 阶段复核短期 access token（查询参数或子协议携带）
	token := r.URL.Query().Get("token")
	if token == "" && len(websocket.Subprotocols(r)) > 0 {
		token = websocket.Subprotocols(r)[0]
	}
	userID, err := s.auth.VerifyToken(token)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s.serveConn(r.Context(), conn, userID)
}

// serveConn 单连接生命周期：读循环（本 goroutine）+ 写循环（独立 goroutine）。
func (s *WSServer) serveConn(ctx context.Context, conn *websocket.Conn, userID int64) {
	// B22 建连读取行情权益（Redis user_vip 缓存；未命中按 free，复核器周期兜底）
	planType := domain.PlanFree
	if s.userVip != nil {
		if pt, ok := s.userVip.PlanType(ctx, userID); ok {
			planType = pt
		}
	}
	c := &application.Conn{
		UserID:    userID,
		Subs:      domain.NewSubs(s.maxSubs),
		Quotes:    domain.NewQuoteBuffer(),
		KlineCh:   make(chan wsproto.KlinePush, 4),
		KlineSubs: domain.NewKlinePeriods(),
		AlertCh:   make(chan wsproto.AlertPush, 256),
		SysCh:     make(chan wsproto.SysData, 8),
		Done:      make(chan struct{}),
	}
	c.Ent.Store(domain.NewEntitlement(planType))
	s.hub.Register(c)
	defer func() {
		s.hub.Unregister(c)
		c.CloseOnceClose()
		_ = conn.Close()
	}()

	writeDone := make(chan struct{})
	go s.writeLoop(conn, c, writeDone)

	defer func() { <-writeDone }()
	conn.SetReadLimit(4096)
	_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	})

	limiter := domain.NewFrameLimiter(s.frameRate)
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.Done:
			return
		default:
		}
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if !limiter.Allow(time.Now()) {
			s.log.Warn("frame rate exceeded", "user_id", userID)
			return // 冻结契约：超过帧速率上限关闭连接
		}
		var msg wsproto.ClientMsg
		if err := jsonUnmarshal(data, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case wsproto.TypePing:
			_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		case wsproto.TypeSub:
			// 逐个登记实际被 Subs 接纳的 symbol（达上限时部分接纳），Hub 反向索引
			// 与 Subs 精确同步；Redis symgw 索引只登记真实生效的键（避免幽灵路由）
			added := make([]string, 0, len(msg.Symbols))
			for _, sym := range msg.Symbols {
				if c.Subs.Has(sym) {
					continue
				}
				if !c.Subs.Add(sym) {
					break // 冻结上限已满，后续 symbol 跳过
				}
				added = append(added, sym)
			}
			if len(added) > 0 {
				s.hub.LinkSym(c, added...)
				_ = s.subs.Subscribe(ctx, added...)
			}
			// channels 订阅（docs/04 §7 B21 启用预埋字段）：kline@<period_min>，quote@3s 为默认行为无需注册
			// B22 权益门控：free 仅 kline@1m，超档 channel 忽略；vip 全周期
			ent := c.Ent.Load()
			for _, ch := range msg.Channel {
				if period, ok := parseKlineChannel(ch); ok {
					if n, err := strconv.Atoi(period); err == nil && !ent.KlineAllowed(int32(n)) {
						continue // 超档周期（free 的 kline@3m+）忽略
					}
					c.KlineSubs.Add(period)
				}
			}
		case wsproto.TypeUnsub:
			c.Subs.Remove(msg.Symbols...)
			s.hub.UnlinkSym(c, msg.Symbols...)
			_ = s.subs.Unsubscribe(ctx, msg.Symbols...)
			for _, ch := range msg.Channel {
				c.KlineSubs.Remove(klinePeriodOf(ch))
			}
		case wsproto.TypeAck:
			if msg.DeliveryID != "" {
				s.acker.AckAsync(msg.DeliveryID)
			}
		}
	}
}

// writeLoop 写循环：告警高优先级通道 + 周期行情合并帧（丢旧保新，B22 per-conn 差频）。
func (s *WSServer) writeLoop(conn *websocket.Conn, c *application.Conn, done chan<- struct{}) {
	defer close(done)
	// B22 差频：quote 合并帧周期按权益（free 10s / vip 3s），复核器更新后下轮生效。
	// 复用单 Timer（每轮 Reset）而非 time.After：20 万连接目标下逐轮分配一次性
	// timer 的 GC 压力不可忽略；Reset 前排空保证权益动态切换即时生效。
	timer := time.NewTimer(c.Ent.Load().QuoteInterval)
	defer timer.Stop()
	for {
		select {
		case <-c.Done:
			return
		case sys := <-c.SysCh: // 系统通知（drain 等）
			if !s.write(conn, wsproto.TypeSys, sys) {
				return
			}
		case a := <-c.AlertCh: // 告警优先：独立通道不被行情挤占
			if !s.write(conn, wsproto.TypeAlert, a) {
				return
			}
		case k := <-c.KlineCh: // 周期闭合 K 线（B21：低频，直接发）
			if !s.write(conn, wsproto.TypeKline, k) {
				return
			}
		case <-timer.C:
			quotes := c.Quotes.Drain()
			if len(quotes) > 0 && !s.write(conn, wsproto.TypeQuote, quotes) {
				return
			}
		}
		if !timer.Stop() { // 周期重置：每轮按当前权益档位刷新间隔
			select { // 已触发则排空残留值，避免下轮立即空转
			case <-timer.C:
			default:
			}
		}
		timer.Reset(c.Ent.Load().QuoteInterval)
	}
}

func (s *WSServer) write(conn *websocket.Conn, typ string, data any) bool {
	payload, err := wsproto.Encode(typ, data)
	if err != nil {
		return true
	}
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return conn.WriteMessage(websocket.TextMessage, payload) == nil
}

// klineChannelPeriods channels 命名（docs/04 §7 B21）：kline@<period_min>。
// 值 = 分钟数；未列入枚举的周期返回 false（忽略该 channel）。
var klineChannelPeriods = map[string]int32{
	"kline@1m": 1, "kline@3m": 3, "kline@5m": 5, "kline@15m": 15,
	"kline@30m": 30, "kline@60m": 60, "kline@1d": 1440,
}

func parseKlineChannel(ch string) (string, bool) {
	p, ok := klineChannelPeriods[ch]
	if !ok {
		return "", false
	}
	return strconv.FormatInt(int64(p), 10), true
}

func klinePeriodOf(ch string) string {
	if p, ok := klineChannelPeriods[ch]; ok {
		return strconv.FormatInt(int64(p), 10)
	}
	return ""
}
