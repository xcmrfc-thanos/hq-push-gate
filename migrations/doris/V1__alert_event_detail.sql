-- Doris 告警明细表（U2 启用 Doris 时执行；U0/U1 关闭 Doris，docs/08 §3.0）
CREATE TABLE alert_event_detail (
  event_id     VARCHAR(64) NOT NULL COMMENT 'rule_id + rule_version + trigger_window',
  rule_id      BIGINT NOT NULL,
  rule_version BIGINT NOT NULL,
  market       VARCHAR(16) NOT NULL,
  symbol       VARCHAR(32) NOT NULL,
  title        VARCHAR(128),
  trigger_at   DATETIME NOT NULL,
  detail_json  TEXT,
  ingest_at    DATETIME DEFAULT CURRENT_TIMESTAMP
) UNIQUE KEY (event_id)
DISTRIBUTED BY HASH(rule_id) BUCKETS 8
PROPERTIES ("replication_num" = "1");
