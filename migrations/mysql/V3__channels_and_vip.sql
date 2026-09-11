-- V3：商业化与通知渠道（docs/10 §8；L1 渠道层 + L2 商业层/L3 开放层的表基础）

-- 用户套餐（与 user 1:1；plan_type: free/vip1/vip2/vip3，docs/10 §3.1）
CREATE TABLE user_vip (
  user_id         BIGINT PRIMARY KEY,
  plan_type       VARCHAR(16) NOT NULL DEFAULT 'free',
  expire_time     DATETIME NULL,
  h5_max_sub      INT NOT NULL DEFAULT 5,
  api_max_sub     INT NOT NULL DEFAULT 20,
  allow_sms       TINYINT NOT NULL DEFAULT 0,
  channel_default VARCHAR(8) NOT NULL DEFAULT 'email' COMMENT 'sms|email|dd|feishu',
  dingtalk_webhook VARCHAR(512) NULL COMMENT '钉钉群机器人绑定：URL[|加签secret][|关键词]',
  feishu_webhook   VARCHAR(512) NULL COMMENT '飞书群机器人绑定：URL[|加签secret][|关键词]',
  updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
);

-- 用户邮箱（H5 订阅默认邮箱 / 邮件告警收件地址；退订按 email 反查用户）
ALTER TABLE `user`
  ADD COLUMN email VARCHAR(254) NULL UNIQUE;

-- 告警规则扩展：渠道偏好 + 来源 + 订阅糖衣标识（docs/10 §3.3，不建订阅表）
ALTER TABLE alert_rule
  ADD COLUMN channel_prefer VARCHAR(8) NULL COMMENT 'sms|email，NULL=继承 user_vip.channel_default',
  ADD COLUMN source         VARCHAR(8) NOT NULL DEFAULT 'h5' COMMENT 'h5|api',
  ADD COLUMN subscribe_key  VARCHAR(64) NULL COMMENT 'subscribe API 的订阅标识',
  ADD KEY idx_source (source);

-- 通知发送记录与服务商回执（邮件/短信/开发者 WebHook 统一记录，docs/10 §4.2）
CREATE TABLE notify_send_log (
  id              BIGINT PRIMARY KEY AUTO_INCREMENT,
  user_id         BIGINT NOT NULL,
  rule_id         BIGINT NOT NULL DEFAULT 0,
  event_id        VARCHAR(64) NOT NULL DEFAULT '',
  channel         VARCHAR(8) NOT NULL,             -- mail|sms|webhook
  provider        VARCHAR(32) NOT NULL,            -- smtp|dmail|sms-provider|customer-hook
  recipient       VARCHAR(320) NOT NULL DEFAULT '',-- 收件邮箱/手机号/hook_url
  provider_msg_id VARCHAR(128) NULL,
  status          VARCHAR(16) NOT NULL,            -- SENT|FAIL|DEGRADED|UNSUBSCRIBED
  error           VARCHAR(512) NULL,
  created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
  KEY idx_user_time (user_id, created_at),
  KEY idx_event (event_id),
  KEY idx_recipient (recipient)
);

-- 自检修复：alert_record.id 无自增，notify 的 INSERT 不带 id——
-- INSERT IGNORE 下首条占位 id=0、之后全部静默丢弃（M1 潜伏缺陷）
ALTER TABLE alert_record MODIFY id BIGINT NOT NULL AUTO_INCREMENT;
