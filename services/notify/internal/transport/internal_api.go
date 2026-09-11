// Package transport 内部 API：告警补拉（biz-service 代理 /api/v1/alerts）与 ACK（ws-gateway 回调）。
package transport

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/hqpush/gate/packages/httpx"
	"github.com/hqpush/gate/services/notify/internal/domain"
)

const rfc3339Milli = "2006-01-02T15:04:05.000Z07:00"

// Puller 补拉与 ACK 用例接口。
type Puller interface {
	Pull(ctx context.Context, userID, cursor int64, limit int) ([]*domain.AlertRecord, error)
	Ack(ctx context.Context, deliveryID string) (bool, error)
}

// NewInternalMux 内部 API 路由。统一响应 {code,msg,data}（docs/04 §6）；
// 内部令牌恒时比较 + 40103（docs/11 §3/§4，B17 统一口径）。
func NewInternalMux(p Puller, token string) *http.ServeMux {
	mux := http.NewServeMux()
	auth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Internal-Token")), []byte(token)) != 1 {
				httpx.Err(w, http.StatusUnauthorized, 40103, "unauthorized")
				return
			}
			next(w, r)
		}
	}

	mux.HandleFunc("GET /internal/alerts", auth(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		userID, err := strconv.ParseInt(q.Get("user_id"), 10, 64)
		if err != nil || userID <= 0 {
			httpx.Err(w, http.StatusBadRequest, 40004, "invalid user_id")
			return
		}
		cursor, _ := strconv.ParseInt(q.Get("cursor"), 10, 64)
		limit, _ := strconv.Atoi(q.Get("limit"))
		records, err := p.Pull(r.Context(), userID, cursor, limit)
		if err != nil {
			httpx.Err(w, http.StatusServiceUnavailable, 50302, "pull failed")
			return
		}
		var next int64
		if len(records) > 0 {
			next = records[len(records)-1].CursorID
		}
		data := make([]map[string]any, 0, len(records))
		for _, rec := range records {
			data = append(data, map[string]any{
				"delivery_id": rec.DeliveryID,
				"event_id":    rec.EventID,
				"rule_id":     rec.RuleID,
				"market":      rec.Market,
				"symbol":      rec.Symbol,
				"title":       rec.Title,
				"trigger_at":  rec.TriggerAt.Format(rfc3339Milli),
				"cursor_id":   rec.CursorID,
			})
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"code": 0, "msg": "ok",
			"data": map[string]any{"items": data, "next_cursor": next}})
	}))

	mux.HandleFunc("POST /internal/alerts/ack", auth(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			DeliveryID string `json:"delivery_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DeliveryID == "" {
			httpx.Err(w, http.StatusBadRequest, 40005, "delivery_id required")
			return
		}
		ok, err := p.Ack(r.Context(), req.DeliveryID)
		if err != nil {
			httpx.Err(w, http.StatusServiceUnavailable, 50303, "ack failed")
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"code": 0, "msg": "ok", "data": map[string]bool{"updated": ok}})
	}))

	return mux
}

