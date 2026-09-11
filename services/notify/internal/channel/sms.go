// 短信适配器：阿里云 SMS（dysmsapi）RPC 签名协议（HMAC-SHA1）。
// 凭据仅 env（NOTIFY_SMS_*），未配置时 Send 返回 ErrChannelUnavailable 触发降级（docs/10 §4.1）。
package channel

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type SmsConfig struct {
	Provider        string // aliyun(实装)|tencent|volc|baidu(预留，docs/12 B16 多商)
	AccessKeyID     string // NOTIFY_SMS_AK
	AccessKeySecret string // NOTIFY_SMS_SK
	SignName        string // 短信签名（NOTIFY_SMS_SIGN）
	TemplateCode    string // 模板 code（NOTIFY_SMS_TEMPLATE）
	Endpoint        string // 默认 https://dysmsapi.aliyuncs.com
}

func (c SmsConfig) configured() bool {
	return c.AccessKeyID != "" && c.AccessKeySecret != "" && c.SignName != "" && c.TemplateCode != ""
}

// SmsLookup 短信配置热加载源（ConfigProvider 实现）。
type SmsLookup interface {
	SMS(ctx context.Context) (SmsConfig, bool)
}

type SmsSender struct {
	prov     SmsLookup
	fallback SmsConfig
	hc       *http.Client
}

func NewSmsSender(cfg SmsConfig) *SmsSender {
	ep := cfg.Endpoint
	if ep == "" {
		ep = "https://dysmsapi.aliyuncs.com"
	}
	cfg.Endpoint = ep
	return &SmsSender{fallback: cfg, hc: &http.Client{Timeout: 10 * time.Second}}
}

// NewDynamicSmsSender DB 配置优先（notify_channel 密文表热加载），env 值作 fallback。
func NewDynamicSmsSender(prov SmsLookup, fallback SmsConfig) *SmsSender {
	return NewSmsSender(fallback).withLookup(prov)
}

func (s *SmsSender) withLookup(prov SmsLookup) *SmsSender {
	s.prov = prov
	return s
}

func (s *SmsSender) resolve() SmsConfig {
	cfg := s.fallback
	if s.prov != nil {
		if c, ok := s.prov.SMS(context.Background()); ok {
			if c.Endpoint == "" {
				c.Endpoint = "https://dysmsapi.aliyuncs.com"
			}
			return c
		}
	}
	return cfg
}

func (s *SmsSender) Name() string { return "sms" }

// Send 发送短信；msg.Recipient=手机号，msg.Body=模板参数 JSON（{"code":"xxx"}）。
func (s *SmsSender) Send(_ context.Context, msg OutboundMessage) (string, error) {
	cfg := s.resolve()
	switch strings.ToLower(cfg.Provider) {
	case "", "aliyun":
		// 阿里云 dysmsapi（唯一实装；docs/12 B16 多商预留）
	default:
		return "", fmt.Errorf("%w: sms provider %q reserved (U2 按采购转正)", ErrChannelUnavailable, cfg.Provider)
	}
	if !cfg.configured() {
		return "", ErrChannelUnavailable
	}
	params := map[string]string{
		"AccessKeyId":      cfg.AccessKeyID,
		"Action":           "SendSms",
		"Format":           "JSON",
		"PhoneNumbers":     msg.Recipient,
		"RegionId":         "cn-hangzhou",
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureNonce":   fmt.Sprintf("%d", time.Now().UnixNano()),
		"SignatureVersion": "1.0",
		"SignName":         cfg.SignName,
		"TemplateCode":     cfg.TemplateCode,
		"TemplateParam":    msg.Body,
		"Timestamp":        time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"Version":          "2017-05-25",
	}
	query := canonicalQuery(params)
	// 阿里云 RPC 签名协议固定 HMAC-SHA1（服务商要求，非本地安全选型）
	stringToSign := "GET&%2F&" + url.QueryEscape(query)
	mac := hmac.New(sha1.New, []byte(cfg.AccessKeySecret+"&"))
	mac.Write([]byte(stringToSign))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	endpoint := cfg.Endpoint + "/?Signature=" + url.QueryEscape(signature) + "&" + query
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	resp, err := s.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Code      string `json:"Code"`
		Message   string `json:"Message"`
		BizId     string `json:"BizId"`
		RequestID string `json:"RequestId"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out.Code != "OK" {
		return "", fmt.Errorf("sms provider: code=%s msg=%s", out.Code, out.Message)
	}
	return "sms:" + out.BizId, nil
}

// canonicalQuery 阿里云 RPC 签名的规范化查询串（参数按 key 字典序）。
func canonicalQuery(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+url.QueryEscape(params[k]))
	}
	return strings.Join(parts, "&")
}
