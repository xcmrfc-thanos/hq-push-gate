-- V10 (B27): few-shot 检索飞轮样本表（ADR-042 配套，docs/superpowers/plans B27）。
-- 语料来源：seed=内置样例迁移；online_hit=高频命中；online_unknown=UNKNOWN 拒答回流（人工标注后 ACTIVE）；
-- rule_converted=成功转规则的 condition（正样本）。检索默认关键词重合度，embedding_ref 为向量库升级预留。
-- 安全口径：condition_json 仅存白名单规范化 JSON；无凭据字面量。

CREATE TABLE IF NOT EXISTS fewshot_sample (
  id             BIGINT       NOT NULL AUTO_INCREMENT,
  question       VARCHAR(500) NOT NULL,
  condition_json JSON         NULL,                -- 规范化条件（UNKNOWN 回流待标注时为 NULL）
  source         VARCHAR(20)  NOT NULL,            -- seed / online_hit / online_unknown / rule_converted
  status         VARCHAR(10)  NOT NULL DEFAULT 'PENDING',  -- ACTIVE / PENDING / REJECTED
  embedding_ref  VARCHAR(128) NULL,                -- 向量库升级预留（当前 NULL=关键词检索）
  hit_count      INT          NOT NULL DEFAULT 0,  -- 命中/出现次数（高频排序依据）
  created_at     DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at     DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_fewshot_status (status, source),
  KEY idx_fewshot_question (question)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4;

-- 内置样例 seed（原 fewshot.py 硬编码 8 条迁移入库；ACTIVE 即参与检索注入）
INSERT INTO fewshot_sample (question, condition_json, source, status, hit_count) VALUES
  ('股价大于10元',   '{"type":"PRICE_ABOVE","threshold":10}', 'seed', 'ACTIVE', 0),
  ('跌到5元以下提醒我', '{"type":"PRICE_BELOW","threshold":5}',  'seed', 'ACTIVE', 0),
  ('价格在15到25之间', '{"type":"PRICE_RANGE","low":15,"high":25}', 'seed', 'ACTIVE', 0),
  ('涨幅超过5%的股票', '{"type":"PCT_CHANGE","threshold":5}',   'seed', 'ACTIVE', 0),
  ('今天跌了3个点以上的', '{"type":"PCT_CHANGE","threshold":3,"direction":"down"}', 'seed', 'ACTIVE', 0),
  ('成交量超过100万股', '{"type":"VOLUME_ABOVE","threshold":1000000}', 'seed', 'ACTIVE', 0),
  ('帮我查一下明天会涨停的', NULL, 'seed', 'REJECTED', 0),
  ('优质白马股', NULL, 'seed', 'REJECTED', 0);
