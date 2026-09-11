// 内部压测调压端点：POST /internal/bench/tick-source（X-Internal-Token 鉴权，docs/04 §6）。
package transport

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"

	"github.com/hqpush/gate/packages/httpx"
)

// SimController 可调压模拟源（sim 源实现；其他源为 nil）。
type SimController interface {
	UpdateRate(rate float64)
	Rate() float64
}

type benchReq struct {
	Rate float64 `json:"rate"`
}

func RegisterBench(mux *http.ServeMux, ctrl SimController, token string) {
	mux.HandleFunc("POST /internal/bench/tick-source", func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Internal-Token")), []byte(token)) != 1 {
			httpx.Err(w, http.StatusUnauthorized, 40103, "unauthorized") // 40103 内部令牌（docs/11 §3 注册表）
			return
		}
		if ctrl == nil {
			httpx.Err(w, http.StatusConflict, 40901, "sim source not running")
			return
		}
		var req benchReq
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			httpx.Err(w, http.StatusBadRequest, 40020, "invalid json") // 40020 bench 专用（docs/11 §3，消除与 notify 的 40004 同码复用）
			return
		}
		if req.Rate < 0 || req.Rate > 1_000_000 {
			httpx.Err(w, http.StatusBadRequest, 40021, "rate out of range") // 40021 bench 专用
			return
		}
		ctrl.UpdateRate(req.Rate)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"code": 0, "msg": "ok",
			"data": map[string]float64{"rate": ctrl.Rate()}})
	})
}
