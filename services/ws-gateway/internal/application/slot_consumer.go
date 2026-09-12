package application

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/proto"

	"github.com/hqpush/gate/packages/contract"
	hqv1 "github.com/hqpush/gate/packages/contract/go/hq/v1"
	"github.com/hqpush/gate/packages/contract/wsproto"
	"github.com/hqpush/gate/packages/kafkax"
	"github.com/hqpush/gate/packages/obs"
)

// SlotSink 槽位消息出口：由 Hub 处理（行情分发、告警投递）。
type SlotSink interface {
	DispatchQuote(symKey string, q wsproto.QuotePush) int
	DeliverAlert(userID int64, a wsproto.AlertPush) bool
	DispatchKline(symKey string, periodMin int32, k wsproto.KlinePush) int
}

// SlotConsumer 手动消费本实例租约持有的 ws_push 分区；
// offset 提交到 Redis（at-least-once），消息按 gateway_id fencing 过滤旧实例残留。
type SlotConsumer struct {
	brokers []string
	slot    uint32
	selfID  string
	rdb     RedisOffsetStore
	sink    SlotSink
	log     *slog.Logger

	staleSkipped func()
	quotesSent   func(n int)
	alertsSent   func()
	klinesSent   func(n int)
}

// RedisOffsetStore 分区 offset 存取。
type RedisOffsetStore interface {
	GetSlotOffset(ctx context.Context, slot uint32) (int64, bool, error)
	SetSlotOffset(ctx context.Context, slot uint32, offset int64) error
}

func NewSlotConsumer(brokers []string, slot uint32, selfID string, rdb RedisOffsetStore, sink SlotSink, log *slog.Logger) *SlotConsumer {
	return &SlotConsumer{
		brokers: brokers, slot: slot, selfID: selfID, rdb: rdb, sink: sink, log: log,
	}
}

// WithMetrics 注入指标回调。
func (c *SlotConsumer) WithMetrics(staleSkipped func(), quotesSent func(int), alertsSent func(),
	klinesSent func(n int)) *SlotConsumer {
	c.staleSkipped, c.quotesSent, c.alertsSent, c.klinesSent = staleSkipped, quotesSent, alertsSent, klinesSent
	return c
}

// commitEvery 行情/K线消息的 offset 提交节流阈值（条数）。
// 行情丢旧保新、K线按周期覆盖，崩溃回放 ≤N 条无害；告警不受节流（即时提交，
// 避免重启后整批重投造成客户端重复推送）。
const commitEvery = 1024

// Run 阻塞消费直到 ctx 取消。
func (c *SlotConsumer) Run(ctx context.Context) {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:   c.brokers,
		Topic:     contract.TopicWsPush,
		Partition: int(c.slot),
		MinBytes:  1,
		MaxBytes:  10e6,
		MaxWait:   100 * time.Millisecond,
	})
	defer reader.Close()

	if off, ok, err := c.rdb.GetSlotOffset(ctx, c.slot); err != nil {
		c.log.Error("read slot offset failed", "slot", c.slot, "err", err)
		_ = reader.SetOffset(kafka.LastOffset)
	} else if ok {
		_ = reader.SetOffset(off)
	} else {
		_ = reader.SetOffset(kafka.LastOffset) // 首次接入不回放历史
	}

	var lastCommitted int64 = -1
	var pending int64 = -1
	flush := func(fctx context.Context, offset int64) {
		if offset < 0 || offset == lastCommitted {
			return
		}
		lastCommitted = offset
		if err := c.rdb.SetSlotOffset(fctx, c.slot, offset); err != nil {
			c.log.Error("commit slot offset failed", "slot", c.slot, "err", err)
		}
	}
	for {
		m, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				// 退出冲刷：重启续读不回放大段行情（ctx 已取消，换独立短超时 context）
				fctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				flush(fctx, pending)
				cancel()
				return
			}
			c.log.Error("fetch ws_push failed", "slot", c.slot, "err", err)
			continue
		}
		isAlert := c.handle(ctx, m)
		pending = m.Offset + 1
		if isAlert || pending-lastCommitted >= commitEvery {
			flush(ctx, pending) // 告警即时提交；行情/K线节流提交（幂等消费 + 重启续读）
		}
	}
}

// handle 处理单条 ws_push 消息；返回该消息是否为告警帧（offset 提交策略用）。
func (c *SlotConsumer) handle(ctx context.Context, m kafka.Message) bool {
	var msg hqv1.WsPushMsg
	if err := proto.Unmarshal(m.Value, &msg); err != nil {
		// trace 贯穿：ws_push 消息头 x-trace-id 关联 notify/quote-push 链路日志
		obs.LoggerWithTrace(kafkax.ContextWithTrace(ctx, m), c.log).
			Error("bad ws_push payload", "slot", c.slot, "err", err)
		return false
	}
	// fencing：非本实例的消息直接丢弃（槽位租约切换期间的旧实例残留）
	if msg.GatewayId != c.selfID || msg.GatewaySlot != c.slot {
		if c.staleSkipped != nil {
			c.staleSkipped()
		}
		return false
	}
	switch p := msg.Payload.(type) {
	case *hqv1.WsPushMsg_Quotes:
		for _, t := range p.Quotes.Ticks {
			q := wsproto.QuotePush{
				Symbol: t.Market.String() + ":" + t.Symbol,
				Last:   t.LastPrice,
				Pct:    pct(t.LastPrice, t.PreClose),
				Vol:    t.Volume,
				TS:     t.TimestampMs,
			}
			if n := c.sink.DispatchQuote(q.Symbol, q); n > 0 && c.quotesSent != nil {
				c.quotesSent(n)
			}
		}
		return false
	case *hqv1.WsPushMsg_Kline:
		k := wsproto.KlinePush{
			Symbol:  p.Kline.Market.String() + ":" + p.Kline.Symbol,
			Period:  p.Kline.PeriodMin,
			BeginTS: p.Kline.BeginTsMs,
			Open:    p.Kline.Open,
			High:    p.Kline.High,
			Low:     p.Kline.Low,
			Close:   p.Kline.Close,
			Volume:  p.Kline.Volume,
			Amount:  p.Kline.Amount,
		}
		if n := c.sink.DispatchKline(k.Symbol, p.Kline.PeriodMin, k); n > 0 && c.klinesSent != nil {
			c.klinesSent(n)
		}
		return false
	case *hqv1.WsPushMsg_Alert:
		a := wsproto.AlertPush{
			DeliveryID: p.Alert.DeliveryId,
			EventID:    p.Alert.EventId,
			RuleID:     ruleFromEventID(p.Alert.EventId),
			Symbol:     p.Alert.Market.String() + ":" + p.Alert.Symbol,
			Title:      p.Alert.Title,
			Detail:     p.Alert.DetailJson,
			TS:         p.Alert.TriggerTs,
		}
		delivered := c.sink.DeliverAlert(p.Alert.UserId, a)
		c.log.Info("alert consumed from ws_push", "user_id", p.Alert.UserId, "rule_id", a.RuleID,
			"delivery_id", a.DeliveryID, "delivered", delivered)
		if delivered && c.alertsSent != nil {
			c.alertsSent()
		}
		return true
	}
	return false
}

func pct(last, preClose float64) float64 {
	if preClose <= 0 {
		return 0
	}
	return (last - preClose) / preClose * 100
}

// ruleFromEventID 从 event_id（r{rule}-v{ver}-w{win}）解析规则 ID；
// AlertDelivery 契约不携带 rule_id（docs/04 §1），WS 协议的 ruleId 由 event_id 还原。
func ruleFromEventID(eventID string) int64 {
	if len(eventID) < 2 || eventID[0] != 'r' {
		return 0
	}
	end := strings.IndexByte(eventID[1:], '-')
	if end < 0 {
		return 0
	}
	n, err := strconv.ParseInt(eventID[1:1+end], 10, 64)
	if err != nil {
		return 0
	}
	return n
}

