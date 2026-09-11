// Package configx 提供环境变量配置加载与校验（docs/08：环境变量大写下划线并按服务前缀）。
package configx

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Env struct{ prefix string }

func New(prefix string) *Env { return &Env{prefix: strings.ToUpper(prefix) + "_"} }

func (e *Env) raw(key, def string) string {
	if v, ok := os.LookupEnv(e.prefix + key); ok && v != "" {
		return v
	}
	return def
}

func (e *Env) Str(key, def string) string { return e.raw(key, def) }

func (e *Env) Int(key string, def int) int {
	v := e.raw(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		panic(fmt.Sprintf("%s%s: %v", e.prefix, key, err))
	}
	return n
}

func (e *Env) Int64(key string, def int64) int64 {
	v := e.raw(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		panic(fmt.Sprintf("%s%s: %v", e.prefix, key, err))
	}
	return n
}

func (e *Env) Float64(key string, def float64) float64 {
	v := e.raw(key, "")
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		panic(fmt.Sprintf("%s%s: %v", e.prefix, key, err))
	}
	return f
}

func (e *Env) Bool(key string, def bool) bool {
	v := e.raw(key, "")
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		panic(fmt.Sprintf("%s%s: %v", e.prefix, key, err))
	}
	return b
}

func (e *Env) Duration(key string, def time.Duration) time.Duration {
	v := e.raw(key, "")
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		panic(fmt.Sprintf("%s%s: %v", e.prefix, key, err))
	}
	return d
}

func (e *Env) List(key string, def []string) []string {
	v := e.raw(key, "")
	if v == "" {
		return def
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Uint32List 解析逗号分隔的无符号整数列表（如槽位配置 "0,1,2"）。
func (e *Env) Uint32List(key string, def []uint32) []uint32 {
	v := e.raw(key, "")
	if v == "" {
		return def
	}
	parts := strings.Split(v, ",")
	out := make([]uint32, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			panic(fmt.Sprintf("%s%s: %v", e.prefix, key, err))
		}
		out = append(out, uint32(n))
	}
	return out
}

// GuardProd 生产凭据门禁（docs/12 §1.1 #18）：HQ_PROFILE=prod 时，任一凭据为空或仍等于
// dev 默认值即 fail-fast 拒绝启动，杜绝弱密钥上线。非 prod 环境不做任何检查。
// entries 每项 = {报告名(建议 env 键名), 当前生效值, dev 默认值}。
func GuardProd(entries ...[3]string) {
	if os.Getenv("HQ_PROFILE") != "prod" {
		return
	}
	var weak []string
	for _, e := range entries {
		name, actual, devDefault := e[0], e[1], e[2]
		if actual == "" || actual == devDefault {
			weak = append(weak, name)
		}
	}
	if len(weak) > 0 {
		panic(fmt.Sprintf("HQ_PROFILE=prod: weak/empty credentials %v —— 必须通过环境变量/K8s Secret 注入强凭据（docs/11 §7）", weak))
	}
}
