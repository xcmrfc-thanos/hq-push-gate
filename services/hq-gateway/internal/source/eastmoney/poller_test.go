package eastmoney

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hqpush/gate/services/hq-gateway/internal/domain"
)

// fakeIngest 记录收到的 tick，并在达到数量时关闭 done。
type fakeIngest struct {
	mu    sync.Mutex
	ticks []*domain.Tick
	done  chan struct{}
	need  int
}

func newFakeIngest(need int) *fakeIngest {
	return &fakeIngest{done: make(chan struct{}), need: need}
}

func (f *fakeIngest) Ingest(_ context.Context, ticks []*domain.Tick) (int, error) {
	f.mu.Lock()
	f.ticks = append(f.ticks, ticks...)
	reached := len(f.ticks) >= f.need
	f.mu.Unlock()
	if reached {
		select {
		case <-f.done:
		default:
			close(f.done)
		}
	}
	return 0, nil
}

func (f *fakeIngest) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.ticks)
}

const oneTick = `{"data":{"diff":[{"f12":"600000","f13":1,"f2":12.34,"f15":12.5,"f16":12.1,"f17":12.2,"f18":12.0,"f5":1,"f6":1,"f124":1725000000}]}}`

// failingThenOK RoundTripper：前 failN 次返回 500，之后返回正常快照。
func failingThenOK(failN *int) rtFunc {
	return func(*http.Request) (*http.Response, error) {
		if *failN > 0 {
			*failN--
			return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return respJSON(oneTick)
	}
}

func TestPollerRecoversAfterFailures(t *testing.T) {
	failN := 3
	ingest := newFakeIngest(1)
	c := newClientWith(failingThenOK(&failN))

	p := NewPoller(c, ingest, slog.Default(), []string{"600000"},
		1*time.Millisecond, // interval
		0,                  // minGap
		1*time.Millisecond, // backoff base
		5*time.Millisecond, // backoff max
		80, false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = p.Run(ctx) }()

	select {
	case <-ingest.done:
		if failN != 0 {
			t.Errorf("失败次数未耗尽: %d", failN)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("退避恢复失败，仅收到 %d 个 tick", ingest.total())
	}
}

func TestPollerStopsOnContextCancel(t *testing.T) {
	ingest := newFakeIngest(1 << 30) // 永不满足
	c := newClientWith(failingThenOK(new(int)))
	p := NewPoller(c, ingest, nil, []string{"600000"}, time.Millisecond, 0, time.Millisecond, time.Millisecond, 80, false)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("取消后应返回 nil，得到 %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ctx 取消后 Run 未退出")
	}
}

func TestBackoffCurve(t *testing.T) {
	fn := backoffFn(1*time.Second, 5*time.Minute)
	lower := map[int]time.Duration{
		1:  1 * time.Second, // base
		2:  2 * time.Second, // base<<1
		3:  4 * time.Second,
		10: 5 * time.Minute, // base*2^9=512s 触发封顶 5m
	}
	for n, lo := range lower {
		hi := lo + lo/2 // 抖动上限 +50%
		if got := fn(n); got < lo || got > hi {
			t.Errorf("backoff(%d)=%v 超出 [%v,%v]", n, got, lo, hi)
		}
	}
}

func TestInSession(t *testing.T) {
	// 2026-09-07 是周一，2026-09-12 是周六
	mon := func(h, m int) time.Time {
		return time.Date(2026, 9, 7, h, m, 0, 0, time.FixedZone("CST", 8*3600))
	}
	if !inSession(mon(10, 0)) {
		t.Error("周一 10:00 应在交易时段")
	}
	if !inSession(mon(14, 30)) {
		t.Error("周一 14:30 应在交易时段")
	}
	if inSession(mon(8, 0)) || inSession(mon(12, 10)) || inSession(mon(16, 0)) {
		t.Error("非交易时段判断错误（盘前/午休/盘后）")
	}
	sat := time.Date(2026, 9, 12, 10, 0, 0, 0, time.FixedZone("CST", 8*3600))
	if inSession(sat) {
		t.Error("周六不应在交易时段")
	}
}
