#!/usr/bin/env python3
"""东财近期 1m K线回补：push2his 免费接口 → ClickHouse kline_local（period_min=1）。

仅承担近 ~5 个交易日（接口上限 1200 根），深历史由 baostock_5m.py 负责（docs/09 §5.1 Branch 4）。
口径：fqt=0 不复权为 CK 规范基准；东财时间标签为 bar 闭合时间，begin_ts 回退一个周期。

用法：
  py -3 scripts/backfill/eastmoney_1m.py --symbols 600000,000001 --days 5
环境：HQ_CLICKHOUSE_URL（默认 http://localhost:23004）
"""
import argparse
import json
import time
import urllib.parse

from netguard import SecurityError, external_get_json_multi, local_ck_insert

ALLOWED_HOSTS = {"push2his.eastmoney.com", "push2delay.eastmoney.com"}
HOSTS = ["push2his.eastmoney.com", "push2delay.eastmoney.com"]
FIELDS = "f51,f52,f53,f54,f55,f56,f57"  # time,open,close,high,low,volume,amount


def secid(symbol: str) -> str:
    if len(symbol) == 6 and symbol[0] in "0134689":
        return ("1." if symbol[0] == "6" else "0.") + symbol
    raise ValueError(f"unsupported symbol {symbol}")


def fetch_klines(symbol: str, days: int) -> list[dict]:
    qs = urllib.parse.urlencode({
        "secid": secid(symbol), "klt": 1, "fqt": 0,
        "lmt": str(days * 240), "end": "20500101", "fields1": "f1,f2,f3",
        "fields2": FIELDS,
    })
    urls = [f"https://{h}/api/qt/stock/kline/get?{qs}" for h in HOSTS]
    data = external_get_json_multi(urls, ALLOWED_HOSTS).get("data") or {}
    bars = []
    for row in data.get("klines") or []:
        p = row.split(",")
        close_ts = int(time.mktime(time.strptime(p[0], "%Y-%m-%d %H:%M"))) * 1000
        bars.append({
            "market": "A_SHARE", "symbol": symbol, "period_min": 1,
            # bar 闭合时间标签回退一个周期得到开始时间（09:31 标签 = 09:30 开始）
            "begin_ts": close_ts - 60_000,
            "open": float(p[1]), "close": float(p[2]),
            "high": float(p[3]), "low": float(p[4]),
            "volume": float(p[5]) * 100,  # 东财 kline f56 单位为手，×100 转股（与实时链路对齐）
            "amount": float(p[6]),
        })
    return bars


def insert_ck(base_url: str, bars: list[dict], version: int) -> None:
    if not bars:
        return
    sql = ("INSERT INTO kline_local"
           " (event_id, ingest_version, market, symbol, period_min, begin_ts,"
           "  open, high, low, close, volume, amount) FORMAT JSONEachRow")
    rows = []
    for b in bars:
        b = dict(b)
        b["event_id"] = f"{b['market']}:{b['symbol']}:{b['period_min']}:{b['begin_ts']}"
        b["ingest_version"] = version
        rows.append(json.dumps(b, separators=(",", ":")))
    local_ck_insert(base_url, sql, "\n".join(rows) + "\n")


def main() -> None:
    import os
    ap = argparse.ArgumentParser()
    ap.add_argument("--symbols", required=True, help="逗号分隔 A 股代码")
    ap.add_argument("--days", type=int, default=5, help="回补天数（接口上限约 5 交易日）")
    ap.add_argument("--gap", type=float, default=0.8, help="请求间隔秒（频控）")
    ap.add_argument("--ck", default=os.environ.get("HQ_CLICKHOUSE_URL", "http://localhost:23004"))
    args = ap.parse_args()

    version = int(time.time() * 1_000_000)
    symbols = [s.strip() for s in args.symbols.split(",") if s.strip()]
    for i, sym in enumerate(symbols):
        try:
            bars = fetch_klines(sym, args.days)
            insert_ck(args.ck, bars, version)
            print(f"{sym}: {len(bars)} bars -> CK")
        except (SecurityError, Exception) as exc:  # noqa: BLE001 单标的失败不阻断
            print(f"{sym}: FAILED {exc}")
        if i < len(symbols) - 1:
            time.sleep(args.gap)


if __name__ == "__main__":
    main()
