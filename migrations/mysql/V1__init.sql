-- hq-push-gate 业务库初始化（MySQL 8.0, utf8mb4）
-- 契约来源：docs/04-数据模型与接口契约.md §3

CREATE TABLE market_dict (            -- 市场元数据（Flink 窗口参数来源）
  market        VARCHAR(16) PRIMARY KEY,
  name          VARCHAR(32) NOT NULL,
  timezone      VARCHAR(32) NOT NULL,
  sessions_json JSON        NOT NULL,  -- 交易时段/交易日历引用
  tick_size     DECIMAL(10,4) NOT NULL,
  enabled       TINYINT DEFAULT 1
);

CREATE TABLE symbol_meta (
  market  VARCHAR(16) NOT NULL,
  symbol  VARCHAR(32) NOT NULL,
  name    VARCHAR(64) NOT NULL,
  PRIMARY KEY (market, symbol)
);

CREATE TABLE `user` (
  id            BIGINT PRIMARY KEY,
  username      VARCHAR(64) NOT NULL UNIQUE,
  password_hash VARCHAR(128) NOT NULL,
  role          VARCHAR(16) NOT NULL DEFAULT 'USER',  -- USER/ADMIN
  status        TINYINT DEFAULT 1,
  created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE alert_rule (
  id           BIGINT PRIMARY KEY,
  user_id      BIGINT NOT NULL,
  market       VARCHAR(16) NOT NULL,
  symbol       VARCHAR(32) NOT NULL,
  rule_type    VARCHAR(32) NOT NULL,
  `condition`  JSON NOT NULL,          -- 结构化条件，与 RuleMsg.condition 一致
  cooldown_sec INT DEFAULT 60,
  status       TINYINT DEFAULT 1,      -- 1启用 0停用
  version      BIGINT NOT NULL DEFAULT 1,
  KEY idx_user (user_id),
  KEY idx_symbol (market, symbol, status)
);

CREATE TABLE outbox_event (             -- 业务事务与 Kafka 发布的可靠桥接
  id              BIGINT PRIMARY KEY,
  aggregate_type  VARCHAR(32) NOT NULL, -- RULE / GROUP / SUBSCRIPTION
  aggregate_id    BIGINT NOT NULL,
  event_type      VARCHAR(64) NOT NULL, -- RULE_UPSERT / RULE_DELETE
  event_key       VARCHAR(128) NOT NULL,
  payload         JSON NOT NULL,        -- 与 RuleMsg 等公共契约对应的业务载荷
  status          VARCHAR(16) NOT NULL DEFAULT 'PENDING', -- PENDING/PUBLISHED/FAILED
  retry_count     INT NOT NULL DEFAULT 0,
  next_retry_at   DATETIME(3) NOT NULL,
  published_at    DATETIME(3) NULL,
  last_error      VARCHAR(512) NULL,
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_event_key (event_key),
  KEY idx_publish (status, next_retry_at, id)
); -- 与业务写入同一事务；outbox-publisher 成功发 Kafka 后再标记 PUBLISHED

CREATE TABLE alert_record (             -- 用户投递记录（notify 内嵌 alert-inbox 的落库表）
  id          BIGINT PRIMARY KEY,
  delivery_id VARCHAR(96) NOT NULL UNIQUE,  -- event_id + user_id
  event_id    VARCHAR(64) NOT NULL,
  rule_id     BIGINT NOT NULL,
  user_id     BIGINT NOT NULL,
  market      VARCHAR(16) NOT NULL,
  symbol      VARCHAR(32) NOT NULL,
  title       VARCHAR(128),
  trigger_at  DATETIME(3) NOT NULL,
  status      VARCHAR(16) NOT NULL DEFAULT 'PENDING', -- PENDING/SENT/ACKED/EXPIRED
  cursor_id   BIGINT NOT NULL,          -- 全局投递序号（单调递增，补拉游标）
  KEY idx_user_time (user_id, trigger_at),
  KEY idx_user_cursor (user_id, cursor_id, status),
  KEY idx_event (event_id)
); -- 运维口径：按日滚动清理，保留 72h；超窗未 ACK 置 EXPIRED；明细由 ingest-worker 归档 Doris

CREATE TABLE watch_group (
  id BIGINT PRIMARY KEY,
  user_id BIGINT NOT NULL,
  name VARCHAR(64) NOT NULL,
  group_type TINYINT DEFAULT 0,  -- 0手动 1条件自动
  `condition` JSON NULL,
  KEY idx_user (user_id)
);

CREATE TABLE watch_group_item (
  group_id BIGINT NOT NULL,
  market VARCHAR(16) NOT NULL,
  symbol VARCHAR(32) NOT NULL,
  added_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (group_id, market, symbol)
);

-- cursor_id 全局单调递增序号（补拉游标来源）
CREATE TABLE alert_cursor_seq (
  id TINYINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  next_cursor_id BIGINT NOT NULL DEFAULT 1
);
INSERT INTO alert_cursor_seq (id, next_cursor_id) VALUES (1, 1);
