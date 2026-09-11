#!/usr/bin/env python3
"""每日 15:30 K线对账：东财日K vs ClickHouse 1m 滚动日K，按复权基准分组比较。

为什么按基准分组（docs/09 §5.1）：CK 规范存储为不复权（fqt=0）；源请求前复权（fqt=1）时
历史 bar 随因子变化整体漂移，跨基准直接比较必出假差异。本脚本仅在"两侧同基准"的分组内
报告差异，其余分组降级为 advisory（CK 无该基准存储 ≠ 数据缺失）。

输出：data/reconcile/report-<时间戳>.json（含缺口补拉清单）。
用法：
  py -3 scripts/reconcile/kline_reconcile.py --symbols 600000,000001 --days 10
"""
import argparse
import json
import time
import urllib.parse
from pathlib import Path

import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "backfill"))
from netguard import external_get_json_multi, local_ck_query  # noqa: E402

ALLOWED_HOSTS = {"push2his.eastmoney.com", "push2delay.eastmoney.com"}
HOSTS = ["push2his.eastmoney.com", "push2delay.eastmoney.com"]
REPORT_DIR = Path(__file__).resolve().parents[2] / "data" / "reconcile"
PRICE_TOL = 1e-6

DAILY_SQL = ("SELECT toStartOfDay(toDateTime(intDiv(begin_ts,1000),'Asia/Shanghai')) AS d,"
             " argMin(open, begin_ts) AS open, max(high) AS high, min(low) AS low,"
             " argMax(close, begin_ts) AS close"
             " FROM kline_local WHERE market='A_SHARE' AND symbol={sym:String}"
             " AND period_min=1 GROUP BY d ORDER BY d DESC LIMIT {lim:UInt32} FORMAT JSON")


def secid(symbol: str) -> str:
    return ("1." if symbol[0] == "6" else "0.") + symbol


def fetch_source_daily(symbol: str, days: int, fqt: int) -> dict[int, dict]:
    """东财日K（klt=101）。返回 {day_ts: bar}；复权基准由 fqt 决定（0 不复权 / 1 前复权）。"""
    qs = urllib.parse.urlencode({
        "secid": secid(symbol), "klt": 101, "fqt": fqt,
        "lmt": str(days), "end": "20500101", "fields1": "f1,f2,f3",
        "fields2": "f51,f52,f53,f54,f55,f56,f57",
    })
    urls = [f"https://{h}/api/qt/stock/kline/get?{qs}" for h in HOSTS]
    data = external_get_json_multi(urls, ALLOWED_HOSTS).get("data") or {}
    out: dict[int, dict] = {}
    for row in data.get("klines") or []:
        p = row.split(",")
        day = time.strptime(p[0], "%Y-%m-%d")
        key = int(time.mktime(day)) * 1000
        out[key] = {"open": float(p[1]), "high": float(p[3]),
                    "low": float(p[4]), "close": float(p[2])}
    return out


def ck_daily(base_url: str, symbol: str, days: int) -> dict[int, dict]:
    """CK 1m 滚动日K（不复权基准；argMin/argMax 定首尾）。参数化查询，symbol 不入 SQL 文本。"""
    params = {"query": DAILY_SQL, "param_sym": symbol, "param_lim": str(days)}
    resp = local_ck_query(base_url, params)
    rows = resp.get("data", []) if isinstance(resp, dict) else resp
    out: dict[int, dict] = {}
    for r in rows:
        d = time.strptime(r["d"], "%Y-%m-%d %H:%M:%S")
        key = int(time.mktime(d)) * 1000
        out[key] = {"open": float(r["open"]), "high": float(r["high"]),
                    "low": float(r["low"]), "close": float(r["close"])}
    return out


def compare(symbol: str, src: dict, dst: dict) -> list[dict]:
    issues = []
    for day, sb in sorted(src.items()):
        db = dst.get(day)
        day_str = time.strftime("%Y-%m-%d", time.localtime(day))
        if db is None:
            issues.append({"symbol": symbol, "day": day_str,
                           "type": "missing_in_ck", "basis": "raw"})
            continue
        for k in ("open", "high", "low", "close"):
            if abs(sb[k] - db[k]) > PRICE_TOL * max(1.0, abs(sb[k])):
                issues.append({"symbol": symbol, "day": day_str,
                               "type": f"mismatch_{k}", "basis": "raw",
                               "source": sb[k], "ck": db[k]})
    return issues


def main() -> None:
    import os
    ap = argparse.ArgumentParser()
    ap.add_argument("--symbols", required=True)
    ap.add_argument("--days", type=int, default=10, help="对账回看交易日数")
    ap.add_argument("--ck", default=os.environ.get("HQ_CLICKHOUSE_URL", "http://localhost:23004"))
    args = ap.parse_args()

    issues: list[dict] = []
    advisory = []
    symbols = [s.strip() for s in args.symbols.split(",") if s.strip()]
    for i, sym in enumerate(symbols):
        # 基准分组：raw（fqt=0）为两侧共同基准，参与硬比较；前复权（fqt=1）CK 无独立存储，
        # 只做 advisory，不与 raw 数据比较（避免复权漂移假差异）
        src_raw = fetch_source_daily(sym, args.days, fqt=0)
        dst_raw = ck_daily(args.ck, sym, args.days)
        issues.extend(compare(sym, src_raw, dst_raw))
        src_fq = fetch_source_daily(sym, args.days, fqt=1)
        if src_fq and not dst_raw:
            advisory.append({"symbol": sym, "basis": "fq1", "note": "no ck coverage"})
        if i < len(symbols) - 1:
            time.sleep(0.8)

    report = {
        "generated_at": time.strftime("%Y-%m-%dT%H:%M:%S"),
        "lookback_days": args.days,
        "symbols": symbols,
        "issues": issues,
        "advisory": advisory,
        # 缺口补拉清单：直接重跑 eastmoney_1m.py --symbols <refill>
        "refill": sorted({x["symbol"] for x in issues if x["type"] == "missing_in_ck"}),
    }
    REPORT_DIR.mkdir(parents=True, exist_ok=True)
    out = REPORT_DIR / f"report-{time.strftime('%Y%m%d-%H%M%S')}.json"
    out.write_text(json.dumps(report, ensure_ascii=False, indent=1), encoding="utf-8")
    print(f"issues={len(issues)} advisory={len(advisory)} refill={len(report['refill'])}")
    print(f"report: {out}")


if __name__ == "__main__":
    main()
