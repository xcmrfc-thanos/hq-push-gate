// Package kafkax 封装 Kafka 生产/消费适配：protobuf 编解码、分区键与死信投递。
package kafkax

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/proto"

	"github.com/hqpush/gate/packages/contract"
	"github.com/hqpush/gate/packages/obs"
)

// Writer 线程安全的 Kafka 生产者。
type Writer struct {
	w   *kafka.Writer
	log *slog.Logger
}

func NewWriter(brokers []string, log *slog.Logger) *Writer {
	return &Writer{
		w: &kafka.Writer{
			Addr:         kafka.TCP(brokers...),
			Balancer:     NewSlotAwareBalancer(),           // ws_push 按 key=slot 精确分区；其余按 key 哈希
			RequiredAcks: kafka.RequireOne,                 // U0 单副本；U2 起 RF=3 + acks=all
			BatchTimeout: 10 * time.Millisecond,
			Async:        false,
		},
		log: log,
	}
}

// PublishProto 以 protobuf 序列化写入，key 决定分区（如 tick 用 market:symbol，ws_push 用 gateway_slot）。
// context 中携带 trace_id 时自动写入 x-trace-id 消息头（docs/08 §7.1.3）。
func (k *Writer) PublishProto(ctx context.Context, topic, key string, msg proto.Message) error {
	val, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal %T: %w", msg, err)
	}
	return k.w.WriteMessages(ctx, kafka.Message{Topic: topic, Key: []byte(key), Value: val, Headers: traceHeaders(ctx, nil)})
}

// PublishRaw 写入原始 payload（如死信封装）。
func (k *Writer) PublishRaw(ctx context.Context, topic, key string, value []byte, headers ...kafka.Header) error {
	return k.w.WriteMessages(ctx, kafka.Message{Topic: topic, Key: []byte(key), Value: value, Headers: traceHeaders(ctx, headers)})
}

// traceHeaders 将 context 中的 trace_id 追加为消息头（已有 x-trace-id 时不覆盖）。
func traceHeaders(ctx context.Context, hdrs []kafka.Header) []kafka.Header {
	if id := obs.TraceFromCtx(ctx); id != "" {
		for _, h := range hdrs {
			if h.Key == obs.TraceHeader {
				return hdrs
			}
		}
		return append(hdrs[:len(hdrs):len(hdrs)], kafka.Header{Key: obs.TraceHeader, Value: []byte(id)})
	}
	return hdrs
}

// ContextWithTrace 从 Kafka 消息头提取 trace_id 存入 context（消费侧入口用）。
func ContextWithTrace(ctx context.Context, m kafka.Message) context.Context {
	for _, h := range m.Headers {
		if h.Key == obs.TraceHeader && len(h.Value) > 0 {
			return obs.WithTrace(ctx, string(h.Value))
		}
	}
	return ctx
}

func (k *Writer) Close() error { return k.w.Close() }

// DeadLetter 将失败消息转投死信 Topic，附加来源与错误头，保证可观测、可重放、可审计。
func (k *Writer) DeadLetter(ctx context.Context, sourceTopic string, orig kafka.Message, reason error) {
	hdrs := append(orig.Headers[:0:0],
		kafka.Header{Key: "x-source-topic", Value: []byte(sourceTopic)},
		kafka.Header{Key: "x-reason", Value: []byte(reason.Error())},
		kafka.Header{Key: "x-deadline", Value: []byte(time.Now().UTC().Format(time.RFC3339))},
	)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := k.PublishRaw(ctx, contract.TopicDeadLetter, string(orig.Key), orig.Value, hdrs...); err != nil {
		k.log.Error("publish dead letter failed", "source", sourceTopic, "err", err)
	}
}

// ReaderConfig 消费者配置。组内消费使用 GroupID；ws_push 分区手动消费使用 Slot。
type ReaderConfig struct {
	Brokers []string
	Group   string // 消费组（组消费模式必填）
	Topic   string
	Slot    int  // >=0 表示手动消费 ws_push 的该分区（槽位）
	First   bool // true 从最早 offset 开始
}

// NewReader 创建组消费 Reader（自动分区均衡、组内提交）。
func NewReader(cfg ReaderConfig) *kafka.Reader {
	return kafka.NewReader(kafka.ReaderConfig{
		Brokers:     cfg.Brokers,
		GroupID:     cfg.Group,
		GroupTopics: []string{cfg.Topic},
		MinBytes:    1,
		MaxBytes:    10e6,
		StartOffset: kafka.FirstOffset,
	})
}

// NewSlotReader 创建 ws_push 单分区手动消费 Reader（槽位租约持有者专用）。
// offset 管理由调用方负责（Redis 提交），保证实例重启后从上次进度继续。
func NewSlotReader(cfg ReaderConfig, startOffset int64) *kafka.Reader {
	return kafka.NewReader(kafka.ReaderConfig{
		Brokers:   cfg.Brokers,
		Topic:     cfg.Topic,
		Partition: cfg.Slot,
		MinBytes:  1,
		MaxBytes:  10e6,
	})
}
