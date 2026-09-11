package application

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// AckForwarder 将客户端 ACK 异步转发给 notify（/internal/alerts/ack），
// 失败退避重试，最终丢弃由补拉兜底（ACK 幂等，不阻塞推送路径）。
type AckForwarder struct {
	url     string
	token   string
	ch      chan string
	client  *http.Client
	log     *slog.Logger
	acked   func()
	failed  func()
}

func NewAckForwarder(notifyURL, token string, log *slog.Logger) *AckForwarder {
	return &AckForwarder{
		url:    notifyURL + "/internal/alerts/ack",
		token:  token,
		ch:     make(chan string, 4096),
		client: &http.Client{Timeout: 3 * time.Second},
		log:    log,
	}
}

func (f *AckForwarder) WithMetrics(acked, failed func()) *AckForwarder {
	f.acked, f.failed = acked, failed
	return f
}

// AckAsync 入队（非阻塞，队列满丢弃：客户端会重发 ACK/补拉幂等）。
func (f *AckForwarder) AckAsync(deliveryID string) {
	select {
	case f.ch <- deliveryID:
	default:
	}
}

// Run 阻塞处理直到 ctx 取消。
func (f *AckForwarder) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-f.ch:
			f.post(ctx, id)
		}
	}
}

func (f *AckForwarder) post(ctx context.Context, deliveryID string) {
	body, _ := json.Marshal(map[string]string{"delivery_id": deliveryID})
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.url, bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-Token", f.token)
		resp, err := f.client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			if f.acked != nil {
				f.acked()
			}
			return
		}
	}
	if f.failed != nil {
		f.failed()
	}
	f.log.Warn("ack forward dropped after retries", "delivery_id", deliveryID)
}
