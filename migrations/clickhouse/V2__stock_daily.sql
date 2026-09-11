-- 日级行情宽表（docs/10 §11.7 stock_daily）：T1 日频指标规则的数据基座。
-- 来源：kline_local 1m 收盘聚合 + 东财日线对账修正；派生列（量比/连涨）由日批任务回填。
CREATE TABLE stock_daily
(
    market    LowCardinality(String),
    symbol    LowCardinality(String),
    date      Date,
    open      Float64,
    high      Float64,
    low       Float64,
    close     Float64,
    pre_close Float64,
    pct_chg   Float64, -- (close-pre_close)/pre_close*100
    volume    Float64, -- 股
    amount    Float64,
    ingest_version UInt64
)
ENGINE = ReplacingMergeTree(ingest_version)
ORDER BY (market, symbol, date);
