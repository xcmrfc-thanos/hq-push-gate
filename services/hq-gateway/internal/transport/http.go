// Package transport HTTP 接入层。
package transport

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/hqpush/gate/packages/httpx"
	"github.com/hqpush/gate/packages/obs"
	"github.com/hqpush/gate/services/hq-gateway/internal/application"
	"github.com/hqpush/gate/services/hq-gateway/internal/domain"
)

// NewMux 构建路由：POST /ingest/ticks 批量接入（模拟源/行情源适配器调用）。
func NewMux(ingest *application.IngestService, maxBatch int, metrics *obs.Metrics, ready func() bool) *http.ServeMux {
	mux := http.NewServeMux()
	metrics.Register(mux, func(context.Context) bool { return ready() })
	mux.HandleFunc("POST /ingest/ticks", func(w http.ResponseWriter, r *http.Request) {
		var ticks []*domain.Tick
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBatch)*512))
		if err := dec.Decode(&ticks); err != nil {
			httpx.Err(w, http.StatusBadRequest, 40001, "invalid json: "+err.Error())
			return
		}
		if len(ticks) == 0 {
			httpx.Err(w, http.StatusBadRequest, 40002, "empty batch")
			return
		}
		if len(ticks) > maxBatch {
			httpx.Err(w, http.StatusBadRequest, 40003, "batch too large")
			return
		}
		rejected, err := ingest.Ingest(r.Context(), ticks)
		if err != nil {
			httpx.Err(w, http.StatusServiceUnavailable, 50301, "publish failed")
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"code": 0, "msg": "ok", "data": map[string]int64{
			"accepted": int64(len(ticks) - rejected), "rejected": int64(rejected),
		}})
	})
	return mux
}

