-- LLM 网关：提供商渠道 + 模型（多提供商加权轮询；Branch 16，docs/12 §2.1；凭据安全 docs/11 §7.2）
-- api_key_enc 仅存密文 v1:<kid>:<base64url(nonce||ct||tag)>；主密钥 HQ_MASTER_KEYS 环境注入，仓库零字面量
CREATE TABLE llm_provider (
  id          BIGINT PRIMARY KEY,               -- 手工分配（seed/admin，按 name 幂等 upsert）
  name        VARCHAR(64) NOT NULL UNIQUE,      -- ark|siliconflow|qwen|deepseek
  base_url    VARCHAR(256) NOT NULL,            -- 仅 http/https，写入与读取时结构化校验（拒环回/私有/保留地址）
  api_key_enc VARCHAR(512) NOT NULL,            -- AES-256-GCM 密文，AAD 绑定 kid
  extra       VARCHAR(512) NULL,                -- JSON 差异化：user_agent/thinking/api_style
  enabled     TINYINT NOT NULL DEFAULT 1,
  created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
);

CREATE TABLE llm_model (
  id          BIGINT PRIMARY KEY,
  provider_id BIGINT NOT NULL,
  model_id    VARCHAR(128) NOT NULL,            -- 上游真实模型 ID：ark-code-latest / deepseek-chat
  model_name  VARCHAR(128) NOT NULL,            -- 展示名
  model_type  VARCHAR(16) NOT NULL,             -- chat|embedding|image（Branch 16 仅实现 chat 消费路径）
  weight      INT NOT NULL DEFAULT 1,           -- 轮询权重 ≥1（渠道 = provider×model）
  max_tokens  INT NOT NULL DEFAULT 2048,
  enabled     TINYINT NOT NULL DEFAULT 1,
  created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  KEY idx_provider (provider_id),
  KEY idx_type_enabled (model_type, enabled),
  UNIQUE KEY uk_provider_model (provider_id, model_id)
);
