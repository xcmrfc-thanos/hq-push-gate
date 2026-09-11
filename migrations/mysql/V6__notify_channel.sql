-- 通知渠道凭据（Branch 16，docs/12 §2.1；凭据安全 docs/11 §7.2）
-- config_enc 仅存密文 v1:<kid>:<base64url(...)>，内容为渠道 JSON：
--   mail: {"host","port","tls_mode":"ssl|starttls|plain","username","password","sender"}
--         （通用 SMTP：QQ smtp.qq.com:465 / 163 smtp.163.com:465 / 阿里 smtpdm / 腾讯 SES 同配置面）
--   sms:  {"provider":"aliyun|tencent|volc|baidu","ak_id","ak_secret","sign","template","endpoint"}
CREATE TABLE notify_channel (
  id          BIGINT PRIMARY KEY,               -- 手工/admin 分配，按 (channel, name) 幂等 upsert
  channel     VARCHAR(8) NOT NULL,              -- mail|sms
  name        VARCHAR(64) NOT NULL,             -- smtpdm|qq-smtp|163-smtp|aliyun-sms|...
  config_enc  VARCHAR(2048) NOT NULL,           -- AES-256-GCM 密文 JSON，AAD 绑定 kid
  enabled     TINYINT NOT NULL DEFAULT 1,
  created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uk_channel_name (channel, name)
);
