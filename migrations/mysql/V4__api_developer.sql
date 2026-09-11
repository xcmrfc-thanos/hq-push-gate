-- 开发者接入配置（docs/10 §6/§7 L3 开放层；Branch 12）
CREATE TABLE api_developer_config (
  user_id     BIGINT PRIMARY KEY,
  access_key  VARCHAR(64) NOT NULL UNIQUE,
  app_secret  VARCHAR(128) NOT NULL,            -- 流水线注入，禁止明文提交
  push_mode   VARCHAR(8) NOT NULL DEFAULT 'ws', -- ws|webhook
  hook_url    VARCHAR(512) NULL,
  push_subscribe_changed TINYINT NOT NULL DEFAULT 0,
  created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);
