package eastmoney

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// rtFunc 假 RoundTripper：不真正联网，返回预备好的响应（或错误），并可检查请求。
type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newClientWith(rt rtFunc) *Client {
	return &Client{http: &http.Client{Transport: rt}}
}

func respJSON(body string) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func TestSecid(t *testing.T) {
	cases := map[string]string{
		"600000": "1.600000", // 沪主板
		"688981": "1.688981", // 科创板
		"000001": "0.000001", // 深主板
		"300750": "0.300750", // 创业板
		"830799": "0.830799", // 北交所
		"AAPL":   "",         // 非六位/非 A 股代码不支持
		"60005":  "",
	}
	for sym, want := range cases {
		if got := Secid(sym); got != want {
			t.Errorf("Secid(%q)=%q want %q", sym, got, want)
		}
	}
}

const snapshotOK = `{
  "rc": 0,
  "data": {
    "total": 3,
    "diff": [
      {"f12":"600000","f13":1,"f2":12.34,"f15":12.5,"f16":12.1,"f17":12.2,"f18":12.0,"f5":12345,"f6":15230000,"f124":1725000000},
      {"f12":"000001","f13":0,"f2":"-","f15":"-","f16":"-","f17":"-","f18":10.5,"f5":"-","f6":"-","f124":1725000000},
      {"f12":"300750","f13":0,"f2":210.55,"f15":212.0,"f16":209.0,"f17":211.0,"f18":208.0,"f5":98765,"f6":207700000,"f124":1725000060}
    ]
  }
}`

func TestSnapshotParse(t *testing.T) {
	var gotPath, gotQuery string
	c := newClientWith(rtFunc(func(r *http.Request) (*http.Response, error) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		return respJSON(snapshotOK)
	}))

	ticks, skipped, err := c.Snapshot(context.Background(), []string{"600000", "000001", "300750", "BAD"})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if gotPath != pathQuote {
		t.Errorf("path=%q want %q", gotPath, pathQuote)
	}
	for _, key := range []string{"fltt=2", "np=1", "secids=1.600000%2C0.000001%2C0.300750"} {
		if !strings.Contains(gotQuery, key) {
			t.Errorf("query missing %q: %s", key, gotQuery)
		}
	}
	if len(ticks) != 2 {
		t.Fatalf("ticks=%d want 2（停牌标的应被跳过）", len(ticks))
	}
	if skipped != 1 {
		t.Errorf("skipped=%d want 1", skipped)
	}

	sh := ticks[0]
	if sh.Symbol != "600000" || sh.Exchange != "SSE" || sh.Market != 0 {
		t.Errorf("sh tick = %+v", sh)
	}
	if sh.LastPrice != 12.34 || sh.PreClose != 12.0 || sh.Open != 12.2 {
		t.Errorf("sh prices = %+v", sh)
	}
	if sh.Volume != 1234500 { // 手 → 股
		t.Errorf("volume=%v want 1234500", sh.Volume)
	}
	if sh.TimestampMS != 1725000000000 {
		t.Errorf("ts=%v want 1725000000000", sh.TimestampMS)
	}

	sz := ticks[1]
	if sz.Symbol != "300750" || sz.Exchange != "SZSE" {
		t.Errorf("sz tick = %+v", sz)
	}
}

func TestSnapshotAllowsOnlyWhitelistHost(t *testing.T) {
	c := newClientWith(rtFunc(func(*http.Request) (*http.Response, error) {
		t.Error("不应发出请求")
		return nil, errors.New("unreachable")
	}))
	// 直接通过 do 校验白名单（Snapshot 内部只使用白名单 host）
	if _, err := c.do(context.Background(), "evil.example.com", pathQuote, "a=1"); err == nil {
		t.Fatal("非白名单 host 应报错")
	}
}

func TestSnapshotHTTPError(t *testing.T) {
	c := newClientWith(rtFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusTooManyRequests, Body: io.NopCloser(strings.NewReader(""))}, nil
	}))
	if _, _, err := c.Snapshot(context.Background(), []string{"600000"}); err == nil {
		t.Fatal("429 应返回错误触发退避")
	}
}

func TestSnapshotInvalidCode(t *testing.T) {
	for _, sym := range []string{"BAD", ""} {
		_, _, err := newClientWith(nil).Snapshot(context.Background(), []string{sym})
		if err == nil {
			t.Errorf("无有效代码应报错: %q", sym)
		}
	}
}

func TestFlexFloat(t *testing.T) {
	var f flexFloat
	for in, want := range map[string]float64{
		"12.34": 12.34, `"-"`: 0, "null": 0, "0": 0,
	} {
		if err := f.UnmarshalJSON([]byte(in)); err != nil {
			t.Fatalf("Unmarshal(%s): %v", in, err)
		}
		if float64(f) != want {
			t.Errorf("Unmarshal(%s)=%v want %v", in, f, want)
		}
	}
	if err := f.UnmarshalJSON([]byte(`"abc"`)); err == nil {
		t.Error("非法值应报错")
	}
	_ = fmt.Sprint(f) // 保持 fmt 导入被使用
}
