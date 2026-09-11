-- 开放平台计量（Branch 18，docs/12 §2.2；按调用量计费的数据底座）
-- 高频追加写：AUTO_INCREMENT（V3 notify_send_log 同例），按 (access_key, created_at) 查询
CREATE TABLE open_usage_log (
  id          BIGINT PRIMARY KEY AUTO_INCREMENT,
  access_key  VARCHAR(64) NOT NULL,
  path        VARCHAR(128) NOT NULL,
  status_code INT NOT NULL,
  latency_ms  INT NOT NULL,
  created_at  DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
  KEY idx_ak_time (access_key, created_at)
);
