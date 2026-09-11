// Package httpx 统一信封载体（docs/11 §2.2，B16 后 U1 收尾补做——替换各服务本地 writeErr 重复实现）。
// 信封格式：成功 {code:0, msg:"ok", data}；失败 {code, msg}（data 省略），HTTP status 与 code 分段对齐（docs/11 §3）。
package httpx

import (
	"encoding/json"
	"net/http"
)

// OK 成功信封：code=0，data 必须存在（无内容传空 struct{} / []）。
func OK(w http.ResponseWriter, status int, data any) {
	WriteJSON(w, status, map[string]any{"code": 0, "msg": "ok", "data": data})
}

// Err 失败信封：data 省略，status 为 HTTP 状态码（与 code 分段对齐）。
func Err(w http.ResponseWriter, status, code int, msg string) {
	WriteJSON(w, status, map[string]any{"code": code, "msg": msg})
}

// WriteJSON 通用 JSON 写出。
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
