// e2e 数据面验证工具（联调用）：读 topic 末尾 offset / tail 解码消息。
// 用法：
//   go run ./scripts/e2e offsets <topic>
//   go run ./scripts/e2e tail <topic> <秒数>     # tail alert_event 用 proto 解码，其余按字符串
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/segmentio/kafka-go"

	hqv1 "github.com/hqpush/gate/packages/contract/go/hq/v1"
	"google.golang.org/protobuf/proto"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: e2e offsets <topic> | e2e tail <topic> <seconds>")
		os.Exit(2)
	}
	topic := os.Args[2]
	switch os.Args[1] {
	case "offsets":
		cl := &kafka.Client{Addr: kafka.TCP("127.0.0.1:23003")}
		res, err := cl.Metadata(context.Background(), &kafka.MetadataRequest{Topics: []string{topic}})
		if err != nil {
			fmt.Println("metadata:", err)
			os.Exit(1)
		}
		for _, t := range res.Topics {
			for _, p := range t.Partitions {
				conn, err := kafka.DialLeader(context.Background(), "tcp", "127.0.0.1:23003", t.Name, int(p.ID))
				if err != nil {
					fmt.Printf("%s[%d] err=%v\n", t.Name, p.ID, err)
					continue
				}
				last, _ := conn.ReadLastOffset()
				_ = conn.Close()
				fmt.Printf("%s[%d] last=%d\n", t.Name, p.ID, last)
			}
		}
	case "tail":
		secs, _ := strconv.Atoi(os.Args[3])
		start := kafka.LastOffset
		if os.Getenv("E2E_FROM") == "earliest" {
			start = kafka.FirstOffset
		}
		r := kafka.NewReader(kafka.ReaderConfig{
			Brokers:     []string{"127.0.0.1:23003"},
			Topic:       topic,
			GroupID:     fmt.Sprintf("e2e-%d", time.Now().UnixNano()),
			StartOffset: start,
		})
		defer r.Close()
		deadline := time.Now().Add(time.Duration(secs) * time.Second)
		ctx := context.Background()
		n := 0
		for time.Now().Before(deadline) {
			rctx, cancel := context.WithDeadline(ctx, deadline)
			m, err := r.FetchMessage(rctx)
			cancel()
			if err != nil {
				continue
			}
			n++
			if topic == "alert_event" {
				var e hqv1.AlertEvent
				if proto.Unmarshal(m.Value, &e) == nil {
					fmt.Printf("[alert] id=%s rule=%d ver=%d %s:%s title=%s ts=%d\n",
						e.EventId, e.RuleId, e.Version, e.Market, e.Symbol, e.Title, e.TriggerTs)
					_ = r.CommitMessages(ctx, m)
					continue
				}
			}
			fmt.Printf("[%s] %s\n", topic, string(m.Value[:min(200, len(m.Value))]))
			_ = r.CommitMessages(ctx, m)
		}
		fmt.Printf("tailed %d messages in %ds\n", n, secs)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
