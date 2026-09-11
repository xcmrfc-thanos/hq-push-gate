// 服务商回调入口（docs/10 §5）：EventBridge 邮件事件（投递回执/退订）。
// 原则：验签/校验 + 同步落库快速 200；退订展开为单 UPDATE（轻量）。
// 鉴权：X-Hook-Sign = hex(hmac_sha256(NOTIFY_HOOK_SECRET, body))（生产必须）；
// dev 兜底允许 X-Internal-Token（NOTIFY_INTERNAL_TOKEN）。
package transport

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"

	hqv1 "github.com/hqpush/gate/packages/contract/go/hq/v1"
	"github.com/hqpush/gate/packages/httpx"
	"github.com/hqpush/gate/services/notify/internal/repository"
)

// RuleBcastPublisher 向 rule_bcast 发规则撤销事件的接口（kafkax 适配）。
type RuleBcastPublisher interface {
	PublishRuleDelete(ctx context.Context, msg *hqv1.RuleMsg) error
}

// RegisterHooks 挂载 /internal/hooks/*。
func RegisterHooks(mux *http.ServeMux, store *repository.MySQLStore, bcast RuleBcastPublisher,
	internalToken, hookSecret string, log interface {
		Error(msg string, args ...any)
	}) {
	mux.HandleFunc("POST /internal/hooks/dmail", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			httpx.Err(w, http.StatusBadRequest, 40006, "read body failed")
			return
		}
		if !authHook(r, body, internalToken, hookSecret) {
			httpx.Err(w, http.StatusUnauthorized, 40102, "unauthorized")
			return
		}
		var evt struct {
			Type   string `json:"type"`
			Source string `json:"source"`
			Data   struct {
				ToAddress string `json:"toAddress"`
				Subject   string `json:"subject"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &evt); err != nil || evt.Type == "" {
			httpx.Err(w, http.StatusBadRequest, 40007, "invalid event body")
			return
		}

		switch evt.Type {
		case "dm:Feedback:Unsubscribe":
			res, err := store.UnsubscribeByEmail(r.Context(), evt.Data.ToAddress)
			if err != nil {
				log.Error("unsubscribe by email failed", "err", err)
				httpx.Err(w, http.StatusServiceUnavailable, 50304, "unsubscribe failed") // 503xx→503 分段（docs/11 §3）
				return
			}
			// 关键：向 rule_bcast 发 DELETE，撤销 Flink 广播状态里的规则——
			// 只改 DB 不发 DELETE 的话，退订用户的告警会继续触发（docs/10 §4.4）
			for _, r2 := range res.Rules {
				msg := hqv1.RuleMsg{
					RuleId:      r2.ID,
					Version:     r2.Version + 1,
					Op:          "DELETE",
					Market:      hqv1.Market(hqv1.Market_value[r2.Market]),
					Symbol:      r2.Symbol,
					Type:        hqv1.RuleType(hqv1.RuleType_value[r2.RuleType]),
					Condition:   r2.Condition,
					CooldownSec: int32(r2.CooldownSec),
				}
				if err := bcast.PublishRuleDelete(r.Context(), &msg); err != nil {
					log.Error("publish rule delete failed", "rule_id", r2.ID, "err", err)
				}
			}
			log.Error("", "event", evt.Type, "email", evt.Data.ToAddress, "user", res.UserID, "rules_disabled", len(res.Rules))
		case "dm:Deliver:Succeed", "dm:Deliver:Fail":
			status := "SENT"
			if evt.Type == "dm:Deliver:Fail" {
				status = "FAIL"
			}
			if err := store.RecordDeliveryReceipt(r.Context(), evt.Data.ToAddress, status); err != nil {
				log.Error("record receipt failed", "err", err)
			}
		default:
			// 未知事件类型：200 吸收，避免服务商重试风暴
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"code": 0, "msg": "ok"})
	})

	mux.HandleFunc("POST /internal/hooks/sms", func(w http.ResponseWriter, r *http.Request) {
		// 短信回执（U2 短信适配器接入时扩展；当前 200 空响应避免服务商重试风暴）
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"code": 0, "msg": "ok"})
	})
}

// authHook 鉴权：优先 HMAC 签名（生产），dev 允许内部 token。
func authHook(r *http.Request, body []byte, internalToken, hookSecret string) bool {
	if sig := r.Header.Get("X-Hook-Sign"); sig != "" && hookSecret != "" {
		mac := hmac.New(sha256.New, []byte(hookSecret))
		mac.Write(body)
		expected := hex.EncodeToString(mac.Sum(nil))
		return hmac.Equal([]byte(sig), []byte(expected))
	}
	return internalToken != "" && r.Header.Get("X-Internal-Token") == internalToken
}
