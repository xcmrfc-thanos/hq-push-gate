// Package eastmoney 东方财富免费行情 adapter（免账号，AKShare stock_zh_a_spot_em 同源接口）。
// 仅用于 U0/U1 联调与中小流量（docs/09 §5.1），生产主力源为 MiniQMT/商用源，只换 adapter。
package eastmoney

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/hqpush/gate/services/hq-gateway/internal/domain"
)

// 允许请求的东财域名白名单（硬编码，防 SSRF）：
//   - push2 主域；夜间维护/CDN 黑洞时切换 push2delay（延时快照，AKShare 同款备选）
//   - 1m K线回补（push2his）在 kline-history 分支启用
var hosts = []string{"push2.eastmoney.com", "push2delay.eastmoney.com"}

const (
	pathQuote  = "/api/qt/ulist.np/get"
	timeout    = 4 * time.Second
	maxBodyLen = 1 << 20 // 1MB，快照响应远小于此
)

// Client 东财 HTTP 客户端：固定 https + 域名白名单 + 镜像轮换（粘住最近成功域），
// 不做请求重试（退避由 Poller 负责）。
type Client struct {
	http   *http.Client
	active int32 // hosts 下标：最近成功使用的镜像
}

func NewClient() *Client {
	return &Client{http: &http.Client{Timeout: timeout}}
}

func allowedHost(h string) bool {
	for _, x := range hosts {
		if h == x {
			return true
		}
	}
	return false
}

// Secid 东财证券标识：沪 1.code，深 0.code，北交所 0.code（仅支持 A_SHARE 六位代码）。
func Secid(symbol string) string {
	if len(symbol) != 6 {
		return ""
	}
	switch symbol[0] {
	case '6':
		return "1." + symbol
	case '0', '3', '4', '8', '9':
		return "0." + symbol
	}
	return ""
}

// flexFloat 兼容东财数值字段：正常数字、缺数时返回 "-" 或 null（停牌/无数据）。
type flexFloat float64

func (f *flexFloat) UnmarshalJSON(b []byte) error {
	s := string(b)
	if s == `"-"` || s == "null" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return err
	}
	*f = flexFloat(v)
	return nil
}

// present 数值有效（非 "-" 缺数且大于 0）。
func (f flexFloat) present() bool { return f > 0 }

type snapshotResp struct {
	Data *struct {
		Diff []struct {
			Code     string    `json:"f12"`
			Market   int       `json:"f13"` // 1=沪 0=深/北
			Last     flexFloat `json:"f2"`
			High     flexFloat `json:"f15"`
			Low      flexFloat `json:"f16"`
			Open     flexFloat `json:"f17"`
			PreClose flexFloat `json:"f18"`
			Volume   flexFloat `json:"f5"` // 手（股票 1 手 = 100 股）
			Amount   flexFloat `json:"f6"` // 元
			TS       int64     `json:"f124"`
		} `json:"diff"`
	} `json:"data"`
}

// exchange 由市场号+代码推断交易所。
func exchange(market int, code string) string {
	if market == 1 {
		return "SSE"
	}
	if len(code) > 0 && (code[0] == '4' || code[0] == '8' || code[0] == '9') {
		return "BSE"
	}
	return "SZSE"
}

// Snapshot 批量拉取最新行情快照；缺失/停牌字段（"-"）的标的被跳过并返回跳过数。
// Volume 单位口径：东财 f5 为手，此处 ×100 转为股（与 tick_raw 全链路 volume=股 对齐）。
func (c *Client) Snapshot(ctx context.Context, symbols []string) ([]*domain.Tick, int, error) {
	secids := make([]string, 0, len(symbols))
	for _, s := range symbols {
		if id := Secid(s); id != "" {
			secids = append(secids, id)
		}
	}
	if len(secids) == 0 {
		return nil, 0, fmt.Errorf("no valid symbols")
	}

	q := url.Values{}
	q.Set("fltt", "2")
	q.Set("invt", "2")
	q.Set("np", "1")
	q.Set("fields", "f2,f5,f6,f12,f13,f15,f16,f17,f18,f124")
	q.Set("secids", joinSecids(secids))

	body, err := c.doMirror(ctx, pathQuote, q.Encode())
	if err != nil {
		return nil, 0, err
	}

	var resp snapshotResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, 0, fmt.Errorf("decode snapshot: %w", err)
	}
	if resp.Data == nil {
		return nil, 0, fmt.Errorf("empty snapshot data")
	}

	ticks := make([]*domain.Tick, 0, len(resp.Data.Diff))
	skipped := 0
	for _, d := range resp.Data.Diff {
		// 停牌/缺数标的：不产生 tick（避免污染标准化 rejected 指标）
		if d.Last.present() && d.PreClose.present() && d.TS > 0 {
			ticks = append(ticks, &domain.Tick{
				Market:      domain.MarketAShare,
				Symbol:      d.Code,
				Exchange:    exchange(d.Market, d.Code),
				TimestampMS: d.TS * 1000,
				LastPrice:   float64(d.Last),
				Open:        float64(d.Open),
				High:        float64(d.High),
				Low:         float64(d.Low),
				PreClose:    float64(d.PreClose),
				Volume:      float64(d.Volume) * 100,
				Amount:      float64(d.Amount),
			})
		} else {
			skipped++
		}
	}
	return ticks, skipped, nil
}

func joinSecids(secids []string) string {
	out := ""
	for i, s := range secids {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}

// doMirror 从最近成功的镜像开始依次尝试白名单域名，全部失败才返回错误。
func (c *Client) doMirror(ctx context.Context, path, rawQuery string) ([]byte, error) {
	n := int32(len(hosts))
	start := c.active % n
	var lastErr error
	for i := int32(0); i < n; i++ {
		host := hosts[(start+i)%n]
		body, err := c.do(ctx, host, path, rawQuery)
		if err == nil {
			c.active = (start + i) % n
			return body, nil
		}
		lastErr = err
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
	}
	return nil, lastErr
}

func (c *Client) do(ctx context.Context, host, path, rawQuery string) ([]byte, error) {
	// 出网前置校验：仅允许白名单 https 域名
	if !allowedHost(host) {
		return nil, fmt.Errorf("host %q not allowed", host)
	}
	u := &url.URL{Scheme: "https", Host: host, Path: path, RawQuery: rawQuery}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "hq-push-gate-dev/0.1")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyLen))
	if err != nil {
		return nil, err
	}
	return body, nil
}
