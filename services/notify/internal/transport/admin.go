// 渠道配置管理端点（B16-B，docs/12 §2.1；X-Internal-Token 恒时比较，docs/11 §4）：
// 列表（脱敏）/ 换配置（服务端加密入库，30s 热生效）/ 邮件连通性测试。
package transport

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/hqpush/gate/packages/httpx"
	"github.com/hqpush/gate/packages/secretx"
	"github.com/hqpush/gate/services/notify/internal/channel"
)

// ChannelAdminDeps 渠道管理端点依赖；DB 为 nil 时不注册（无迁移环境下自然缺省）。
type ChannelAdminDeps struct {
	DB    *sql.DB
	Ring  *secretx.KeyRing
	Prov  *channel.ConfigProvider
	Token string
}

// RegisterChannelAdmin 挂载 /internal/v1/channels*（不出公网，APISIX /internal/* 黑洞）。
func RegisterChannelAdmin(mux *http.ServeMux, d ChannelAdminDeps) {
	if d.DB == nil {
		return
	}
	auth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if d.Token == "" || subtle.ConstantTimeCompare([]byte(d.Token), []byte(r.Header.Get("X-Internal-Token"))) != 1 {
				httpx.Err(w, http.StatusUnauthorized, 40103, "unauthorized")
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("GET /internal/v1/channels", auth(func(w http.ResponseWriter, r *http.Request) {
		rows, err := d.DB.QueryContext(r.Context(),
			`SELECT id, channel, name, config_enc, enabled FROM notify_channel ORDER BY channel, id`)
		if err != nil {
			httpx.Err(w, http.StatusServiceUnavailable, 50313, "query failed")
			return
		}
		defer rows.Close()
		items := make([]map[string]any, 0, 4)
		for rows.Next() {
			var id int64
			var ch, name, enc string
			var enabled int
			if err := rows.Scan(&id, &ch, &name, &enc, &enabled); err != nil {
				continue
			}
			cfg := map[string]any{"_decrypt": "failed"}
			if plain, err := d.Ring.Decrypt(enc); err == nil {
				_ = json.Unmarshal([]byte(plain), &cfg)
			}
			for _, k := range []string{"password", "ak_secret", "token"} {
				if v, ok := cfg[k].(string); ok {
					cfg[k] = secretx.MaskSecret(v)
				}
			}
			delete(cfg, "_decrypt")
			items = append(items, map[string]any{
				"id": id, "channel": ch, "name": name, "enabled": enabled == 1, "config": cfg,
			})
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"items": items}})
	}))

	mux.HandleFunc("PUT /internal/v1/channels/mail", auth(func(w http.ResponseWriter, r *http.Request) {
		putChannelConfig(w, r, d, "mail", func(body map[string]any) (map[string]any, bool) {
			cfg := map[string]any{
				"host": body["host"], "port": body["port"], "tls_mode": body["tls_mode"],
				"username": body["username"], "password": body["password"], "sender": body["sender"],
			}
			for _, k := range []string{"host", "username", "password", "sender"} {
				if s, _ := cfg[k].(string); s == "" {
					return nil, false
				}
			}
			return cfg, true
		})
	}))

	mux.HandleFunc("PUT /internal/v1/channels/sms", auth(func(w http.ResponseWriter, r *http.Request) {
		putChannelConfig(w, r, d, "sms", func(body map[string]any) (map[string]any, bool) {
			cfg := map[string]any{
				"provider": body["provider"], "ak_id": body["ak_id"], "ak_secret": body["ak_secret"],
				"sign": body["sign"], "template": body["template"], "endpoint": body["endpoint"],
			}
			for _, k := range []string{"ak_id", "ak_secret", "sign", "template"} {
				if s, _ := cfg[k].(string); s == "" {
					return nil, false
				}
			}
			return cfg, true
		})
	}))

	mux.HandleFunc("POST /internal/v1/channels/mail/test", auth(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			To string `json:"to"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.To == "" {
			httpx.Err(w, http.StatusBadRequest, 40010, "to required")
			return
		}
		cfg, ok := d.Prov.Mail(r.Context())
		if !ok {
			httpx.Err(w, http.StatusServiceUnavailable, 50311, "mail channel not configured")
			return
		}
		_, err := channel.NewMailSender(cfg).Send(r.Context(), channel.OutboundMessage{
			Recipient: req.To,
			Subject:   "【行情告警】渠道配置测试邮件",
			Body:      "notify 渠道配置连通性测试（B16 渠道配置化）。",
		})
		if err != nil {
			httpx.WriteJSON(w, http.StatusBadGateway, map[string]any{"code": 50312, "msg": "send failed: " + err.Error()})
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"code": 0, "msg": "ok", "data": map[string]bool{"sent": true}})
	}))
}

// putChannelConfig 通用 upsert：校验 → JSON → 加密入库 → 失效热加载缓存。
func putChannelConfig(w http.ResponseWriter, r *http.Request, d ChannelAdminDeps,
	ch string, validate func(map[string]any) (map[string]any, bool)) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.Err(w, http.StatusBadRequest, 40010, "invalid json")
		return
	}
	cfg, ok := validate(body)
	if !ok {
		httpx.Err(w, http.StatusBadRequest, 40010, "missing required fields")
		return
	}
	plain, _ := json.Marshal(cfg)
	enc, err := d.Ring.Encrypt(string(plain))
	if err != nil {
		httpx.Err(w, http.StatusServiceUnavailable, 50311, "master key not configured")
		return
	}
	name, _ := body["name"].(string)
	if name == "" {
		name = "default"
	}
	enabled := 1
	if v, ok := body["enabled"].(bool); ok && !v {
		enabled = 0
	}
	ctx := context.Background()
	var id int64
	if err := d.DB.QueryRowContext(ctx,
		`SELECT id FROM notify_channel WHERE channel = ? AND name = ?`, ch, name).Scan(&id); err != nil {
		if err := d.DB.QueryRowContext(ctx,
			`SELECT IFNULL(MAX(id), 0) + 1 FROM notify_channel`).Scan(&id); err != nil {
			httpx.Err(w, http.StatusServiceUnavailable, 50313, "query failed")
			return
		}
		if _, err := d.DB.ExecContext(ctx,
			`INSERT INTO notify_channel (id, channel, name, config_enc, enabled) VALUES (?,?,?,?,?)`,
			id, ch, name, enc, enabled); err != nil {
			httpx.Err(w, http.StatusServiceUnavailable, 50313, "insert failed")
			return
		}
	} else if _, err := d.DB.ExecContext(ctx,
		`UPDATE notify_channel SET config_enc = ?, enabled = ? WHERE id = ?`, enc, enabled, id); err != nil {
		httpx.Err(w, http.StatusServiceUnavailable, 50313, "update failed")
		return
	}
	d.Prov.Reload() // 写后失效：30s TTL 缓存立即回源（热生效）
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"id": id, "channel": ch, "name": name}})
}
