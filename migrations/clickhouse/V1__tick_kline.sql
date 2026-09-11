-- hq-push-gate ClickHouse 时序明细（契约来源：docs/04 §4、docs/07 §8）
-- 幂等约束：event_id + ReplacingMergeTree 版本列 ingest_version；查询不依赖 FINAL。
-- U0 单节点无 Keeper：引擎为非复制 ReplacingMergeTree，读写直接走 *_local 表；
-- 生产集群（k8s-ha，M3）由 CK Operator 建副本，恢复 Replicated 引擎 + Distributed 层。

CREATE TABLE tick_local (
  event_id String,
  ingest_version UInt64,
  market LowCardinality(String),
  symbol LowCardinality(String),
  ts_ms  Int64,
  last_price Float64, open Float64, high Float64, low Float64, pre_close Float64,
  volume Float64, amount Float64
) ENGINE = ReplacingMergeTree(ingest_version)
PARTITION BY (market, toYYYYMM(toDateTime(intDiv(ts_ms,1000))))
ORDER BY (market, symbol, ts_ms, event_id)
TTL toDateTime(intDiv(ts_ms,1000)) + INTERVAL 12 MONTH;

CREATE TABLE kline_local (
  event_id String, ingest_version UInt64,
  market LowCardinality(String), symbol LowCardinality(String),
  period_min UInt16, begin_ts Int64,
  open Float64, high Float64, low Float64, close Float64,
  volume Float64, amount Float64
) ENGINE = ReplacingMergeTree(ingest_version)
PARTITION BY (market, period_min, toYYYYMM(toDateTime(intDiv(begin_ts,1000))))
ORDER BY (market, symbol, period_min, begin_ts, event_id);
