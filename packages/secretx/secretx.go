// Package secretx 渠道凭据加密（docs/11 §7.2）：AES-256-GCM，密文格式 v1:<kid>:<base64url(nonce||ct||tag)>。
//
// 主密钥只从环境变量读取（与 ai-query/app/infrastructure/secrets.py 同格式同口径）：
//   - HQ_MASTER_KEYS="k1=<hex64>[,k0=<hex64>...]"：首个为当前加密密钥，其余仅用于解密（轮换并存）
//   - 兼容 HQ_MASTER_KEY=<hex64>，等价 k1=<hex64>
//
// AAD 绑定 kid，防止密文在不同密钥间混淆移植。源码/测试零凭据字面量。
package secretx

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

// Prefix 密文格式版本前缀。
const Prefix = "v1"

const nonceLen = 12

// ErrSecret 密钥未配置 / 密文格式或校验失败 / base_url 不合规。
var ErrSecret = errors.New("secretx: invalid secret")

// NamedKey 具名主密钥。
type NamedKey struct {
	Kid string
	Key []byte
}

// KeyRing 有序主密钥环：首个为当前加密密钥，其余仅用于解密。
type KeyRing struct{ Keys []NamedKey }

// Lookup 按 kid 取钥。
func (r *KeyRing) Lookup(kid string) ([]byte, bool) {
	if r == nil {
		return nil, false
	}
	for _, k := range r.Keys {
		if k.Kid == kid {
			return k.Key, true
		}
	}
	return nil, false
}

// LoadKeys 从进程环境解析主密钥环。
func LoadKeys() (*KeyRing, error) {
	return LoadKeysWithMap(map[string]string{
		"HQ_MASTER_KEYS": os.Getenv("HQ_MASTER_KEYS"),
		"HQ_MASTER_KEY":  os.Getenv("HQ_MASTER_KEY"),
	})
}

// LoadKeysWithMap 同 LoadKeys，键值由调用方提供（测试注入 / 封装环境源）。
func LoadKeysWithMap(env map[string]string) (*KeyRing, error) {
	raw := strings.TrimSpace(env["HQ_MASTER_KEYS"])
	if raw == "" {
		if legacy := strings.TrimSpace(env["HQ_MASTER_KEY"]); legacy != "" {
			raw = "k1=" + legacy
		}
	}
	if raw == "" {
		return nil, fmt.Errorf("%w: HQ_MASTER_KEYS/HQ_MASTER_KEY not configured", ErrSecret)
	}
	ring := &KeyRing{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kid, hexKey, found := strings.Cut(part, "=")
		if !found {
			kid, hexKey = "k1", part
		}
		kid = strings.TrimSpace(kid)
		key, err := hex.DecodeString(strings.TrimSpace(hexKey))
		if err != nil {
			return nil, fmt.Errorf("%w: master key %s is not hex", ErrSecret, kid)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("%w: master key %s must be 32-byte hex64", ErrSecret, kid)
		}
		ring.Keys = append(ring.Keys, NamedKey{Kid: kid, Key: key})
	}
	if len(ring.Keys) == 0 {
		return nil, fmt.Errorf("%w: no master key parsed", ErrSecret)
	}
	return ring, nil
}

// Encrypt 用当前密钥（环首）加密，返回 v1:<kid>:<base64url>。
func (r *KeyRing) Encrypt(plaintext string) (string, error) {
	if r == nil || len(r.Keys) == 0 {
		return "", ErrSecret
	}
	k := r.Keys[0]
	gcm, err := newGCM(k.Key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nil, nonce, []byte(plaintext), []byte(k.Kid))
	b64 := base64.URLEncoding.EncodeToString(append(nonce, ct...))
	return Prefix + ":" + k.Kid + ":" + b64, nil
}

// Decrypt 按 kid 选钥解密；篡改/错钥/未知 kid 一律 ErrSecret。
func (r *KeyRing) Decrypt(token string) (string, error) {
	parts := strings.SplitN(token, ":", 3)
	if len(parts) != 3 || parts[0] != Prefix {
		return "", ErrSecret
	}
	kid, b64 := parts[1], parts[2]
	key, ok := r.Lookup(kid)
	if !ok {
		return "", ErrSecret
	}
	blob, err := base64.URLEncoding.DecodeString(b64)
	if err != nil || len(blob) <= nonceLen {
		return "", ErrSecret
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	pt, err := gcm.Open(nil, blob[:nonceLen], blob[nonceLen:], []byte(kid))
	if err != nil {
		return "", ErrSecret
	}
	return string(pt), nil
}

// MaskSecret 日志/列表脱敏：abc***last4（docs/11 §7.2）。
func MaskSecret(s string) string {
	if len(s) <= 8 {
		return "***"
	}
	return s[:3] + "***" + s[len(s)-4:]
}

// ValidateBaseURL 结构化 SSRF 防护（写入与读取双侧调用）：仅 http/https；
// 拒绝环回/私有/保留地址与 localhost 变体。只做结构与 IP 字面量校验（不解析 DNS，
// DNS 重绑定层由部署侧出口策略兜底，docs/11 §7.2 注）。
func ValidateBaseURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("%w: base_url must be absolute http/https", ErrSecret)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return "", fmt.Errorf("%w: base_url host rejected: %s", ErrSecret, raw)
	}
	if ip := net.ParseIP(host); ip != nil && !isGlobalIP(ip) {
		return "", fmt.Errorf("%w: base_url private/reserved address rejected: %s", ErrSecret, raw)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// isGlobalIP 近似判定：排除环回/私有/链路本地/组播/未指定。
func isGlobalIP(ip net.IP) bool {
	return !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() && !ip.IsMulticast() && !ip.IsUnspecified()
}

func newGCM(key []byte) (cipher.AEAD, error) {
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(blk)
}
