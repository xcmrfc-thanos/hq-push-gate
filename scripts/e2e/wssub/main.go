// wssub：B21 端到端验证客户端——订阅 symbols + channels，打印收到的帧。
// 用法：go run ./scripts/e2e/wssub -secret dev-jwt-symbol -duration 100s
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	addr := flag.String("addr", "ws://127.0.0.1:23024/ws", "ws-gateway 地址")
	secret := flag.String("secret", "dev-jwt-secret", "HS256 密钥（正式 token）")
	uid := flag.Int("uid", 1, "用户 uid（推送按投递归属路由到对应连接）")
	duration := flag.Duration("duration", 90*time.Second, "订阅时长")
	flag.Parse()

	token := signJWT(*secret, *uid)
	url := *addr + "?token=" + token
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		fmt.Println("dial:", err)
		return
	}
	defer c.Close()

	sub, _ := json.Marshal(map[string]any{
		"type":     "sub",
		"symbols":  []string{"A_SHARE:600000"},
		"channels": []string{"kline@1m", "quote@3s"},
	})
	if err := c.WriteMessage(websocket.TextMessage, sub); err != nil {
		fmt.Println("sub:", err)
		return
	}
	fmt.Println("subscribed: A_SHARE:600000 channels=[kline@1m quote@3s]")

	deadline := time.Now().Add(*duration)
	c.SetReadDeadline(deadline)
	n := map[string]int{}
	for time.Now().Before(deadline) {
		_, msg, err := c.ReadMessage()
		if err != nil {
			fmt.Println("read:", err)
			return
		}
		var envelope struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		_ = json.Unmarshal(msg, &envelope)
		n[envelope.Type]++
		fmt.Printf("E2E-DEBUG frame type=%s len=%d\n", envelope.Type, len(msg)) // TODO(B26-e2e): 观测后移除
		if envelope.Type == "alert" || envelope.Type == "kline" {
			fmt.Printf("E2E-DEBUG payload: %s\n", msg[:min(len(msg), 300)])
		}
	}
	fmt.Printf("counts: quotes=%d kline=%d alert=%d other=%d\n",
		n["quote"], n["kline"], n["alert"], n[""]+n["sys"]+n["pong"])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func base64RawURL(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func hmacSha256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

// signJWT 与 ws-bench 同款（uid=1, exp/typ/sub/key 全 claim）。
func signJWT(secret string, uid int) string {
	b64 := func(b []byte) string { return base64RawURL(b) }
	now := time.Now().Unix()
	head := b64([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := b64([]byte(fmt.Sprintf(`{"uid":%d,"sub":"%d","exp":%d,"typ":"access","key":"hqweb-key"}`, uid, uid, now+3600)))
	mac := hmacSha256([]byte(secret), head+"."+payload)
	return head + "." + payload + "." + b64(mac)
}
