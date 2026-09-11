// ws-bench WS 模拟客户端：批量建立连接、订阅、ACK 告警，验证推送与补拉链路（docs/08 bench 目录）。
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type serverMsg struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type quoteData []struct {
	Symbol string  `json:"symbol"`
	Last   float64 `json:"last"`
	Pct    float64 `json:"pct"`
	TS     int64   `json:"ts"`
}

type alertData struct {
	DeliveryID string `json:"deliveryId"`
	EventID    string `json:"eventId"`
	RuleID     int64  `json:"ruleId"`
	Symbol     string `json:"symbol"`
	Title      string `json:"title"`
	TS         int64  `json:"ts"`
}

func main() {
	addr := flag.String("addr", "ws://127.0.0.1:23024/ws", "ws-gateway 地址")
	users := flag.Int("users", 100, "并发连接数（user id 从 1 开始）")
	symbols := flag.Int("symbols", 5, "每连接订阅 symbol 数")
	duration := flag.Duration("duration", 60*time.Second, "运行时长")
	tokenPrefix := flag.String("token", "dev-token-", "JWT 前缀（联调用）")
	secret := flag.String("secret", "", "HS256 JWT 密钥（设置后按 uid 签发正式 token，与 ws-gateway JWT_SECRET 对齐）")
	channels := flag.String("channels", "", "逗号分隔 channels（如 kline@1m,quote@3s；空=纯 quote）")
	flag.Parse()

	var quotes, alerts, acks, klines, conns int64
	var wg sync.WaitGroup
	stop := make(chan struct{})

	for u := 1; u <= *users; u++ {
		wg.Add(1)
		go func(uid int) {
			defer wg.Done()
			token := *tokenPrefix + jsonInt(uid) // U0 联测 token；正式环境为 JWT
			if *secret != "" {
				token = signJWT(*secret, int64(uid))
			}
			u := url.Values{}
			u.Set("token", token)
			attempt := uint(0)
			for {
				select {
				case <-stop:
					return
				default:
				}
				c, _, err := websocket.DefaultDialer.Dial(*addr+"?"+u.Encode(), nil)
				if err != nil {
					if attempt == 0 { // 首次失败打印原因，便于诊断
						log.Printf("dial failed (uid=%d): %v", uid, err)
					}
					// 指数退避 1s/2s/...上限 64s，连接成功后归零
					backoff := time.Duration(1<<min(attempt, 6)) * time.Second
					select {
					case <-stop:
						return
					case <-time.After(backoff):
					}
					attempt++
					continue
				}
				attempt = 0
				atomic.AddInt64(&conns, 1)
				runConn(c, uid, *symbols, *channels, &quotes, &alerts, &acks, &klines, stop)
				atomic.AddInt64(&conns, -1)
			}
		}(u)
	}

	time.Sleep(*duration)
	close(stop)
	wg.Wait()
	log.Printf("ws-bench done: conns=%d quotes=%d klines=%d alerts=%d acks=%d", conns, quotes, klines, alerts, acks)
}

func runConn(c *websocket.Conn, uid, nSymbols int, channels string, quotes, alerts, acks, klines *int64, stop chan struct{}) {
	defer c.Close()
	subs := make([]string, 0, nSymbols)
	for i := 0; i < nSymbols; i++ {
		subs = append(subs, fmt.Sprintf("A_SHARE:%06d", 600000+i)) // 与模拟源标的对齐
	}
	subMap := map[string]any{"type": "sub", "symbols": subs}
	if channels != "" {
		subMap["channels"] = strings.Split(channels, ",")
	}
	sub, _ := json.Marshal(subMap)
	_ = c.WriteMessage(websocket.TextMessage, sub)

	// 心跳
	go func() {
		ping, _ := json.Marshal(map[string]string{"type": "ping"})
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				_ = c.WriteMessage(websocket.TextMessage, ping)
			}
		}
	}()

	c.SetReadDeadline(time.Now().Add(90 * time.Second))
	for {
		select {
		case <-stop:
			return
		default:
		}
		_, data, err := c.ReadMessage()
		if err != nil {
			log.Printf("read failed (uid=%d): %v", uid, err)
			return
		}
		var m serverMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.Type {
		case "quote":
			var q quoteData
			if json.Unmarshal(m.Data, &q) == nil {
				atomic.AddInt64(quotes, int64(len(q)))
			}
		case "kline":
			atomic.AddInt64(klines, 1)
		case "alert":
			var a alertData
			if json.Unmarshal(m.Data, &a) == nil {
				atomic.AddInt64(alerts, 1)
				ack, _ := json.Marshal(map[string]string{"type": "ack", "deliveryId": a.DeliveryID})
				_ = c.WriteMessage(websocket.TextMessage, ack)
				atomic.AddInt64(acks, 1)
			}
		}
		if rand.Intn(1000) == 0 { // 模拟随机重连
			return
		}
	}
}

func jsonInt(v int) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// signJWT 签发 HS256 token（claims: uid/exp/typ，docs/11 §7.1 契约与 ws-gateway JWTAuth 对齐；stdlib 无新增依赖）。
func signJWT(secret string, uid int64) string {
	b64 := func(b []byte) string {
		return base64.RawURLEncoding.EncodeToString(b)
	}
	now := time.Now().Unix()
	head := b64([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := b64([]byte(fmt.Sprintf(`{"uid":%d,"sub":"%d","exp":%d,"typ":"access","key":"hqweb-key"}`, uid, uid, now+3600)))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(head + "." + payload))
	return head + "." + payload + "." + b64(mac.Sum(nil))
}

func min(a, b uint) uint {
	if a < b {
		return a
	}
	return b
}
