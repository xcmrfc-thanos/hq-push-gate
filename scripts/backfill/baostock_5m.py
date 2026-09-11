#!/usr/bin/env python3
"""Baostock 5m 深历史回填（2011 起）→ ClickHouse kline_local（period_min=5）。

断点续传：data/backfill-progress/baostock_5m.json 记录每标的已完成区间，中断后重跑自动续传。
限速：标的间 sleep；单标的按年分段请求，避免单次超时。
口径：adjustflag=3 不复权为 CK 规范基准（对账按复权基准分组的前提是 CK 单一基准存储）。

用法：
  py -3 scripts/backfill/baostock_5m.py --codes sh.600000,sz.000001 --start 2011-01-01
环境：HQ_CLICKHOUSE_URL（默认 http://localhost:23004）
"""
import argparse
import datetime as dt
import json
import time
import urllib.parse
from pathlib import Path

import baostock as bs

from netguard import local_ck_insert

REPO_ROOT = Path(__file__).resolve().parents[2]
PROGRESS_FILE = REPO_ROOT / "data" / "backfill-progress" / "baostock_5m.json"
FIELDS = "date,time,open,high,low,close,volume,amount"
SQL = ("INSERT INTO kline_local"
       " (event_id, ingest_version, market, symbol, period_min, begin_ts,"
       "  open, high, low, close, volume, amount) FORMAT JSONEachRow")


def load_progress() -> dict:
    try:
        with PROGRESS_FILE.open(encoding="utf-8") as f:
            return json.load(f)
    except OSError:
        return {}


def save_progress(p: dict) -> None:
    PROGRESS_FILE.parent.mkdir(parents=True, exist_ok=True)
    with PROGRESS_FILE.open("w", encoding="utf-8") as f:
        json.dump(p, f, ensure_ascii=False, indent=1)


def year_ranges(start: str, end: str):
    """按自然年切分区间，控制单次请求数据量。"""
    d0, d1 = dt.date.fromisoformat(start), dt.date.fromisoformat(end)
    cur = d0
    while cur <= d1:
        seg_end = min(dt.date(cur.year, 12, 31), d1)
        yield cur.isoformat(), seg_end.isoformat()
        cur = dt.date(cur.year + 1, 1, 1)


def fetch_segment(code: str, start: str, end: str):
    """返回 (bars, last_date)；Baostock time 形如 20260905093500000（bar 闭合标签）。"""
    rs = bs.query_history_k_data_plus(
        code, FIELDS, start_date=start, end_date=end,
        frequency="5", adjustflag="3",  # 5 分钟线（Baostock 参数为 "5" 非 "5m"）
    )
    bars, last_date = [], None
    while rs.error_code == "0" and rs.next():
        row = rs.get_row_data()
        t = row[1]
        day, hm = t[:8], t[8:12]
        close_ts = int(time.mktime(time.strptime(f"{day} {hm}", "%Y%m%d %H%M"))) * 1000
        o, c, h, l = (float(x) if x else 0.0 for x in (row[2], row[5], row[3], row[4]))
        if o <= 0:  # 停牌/无成交段跳过
            continue
        bars.append({
            "market": "A_SHARE",
            "symbol": code.split(".", 1)[1],
            "period_min": 5,
            "begin_ts": close_ts - 5 * 60_000,  # 闭合标签回退一个周期
            "open": o, "high": h, "low": l, "close": c,
            "volume": float(row[6] or 0),  # Baostock 5m 成交量单位为股（无需换算）
            "amount": float(row[7] or 0),
        })
        last_date = day
    return bars, last_date


def main() -> None:
    import sys
    ap = argparse.ArgumentParser()
    ap.add_argument("--codes", required=True, help="逗号分隔，如 sh.600000,sz.000001")
    ap.add_argument("--start", default="2011-01-01")
    ap.add_argument("--end", default=dt.date.today().isoformat())
    ap.add_argument("--gap", type=float, default=1.0, help="标的间 sleep 秒（限速）")
    ap.add_argument("--ck", default="http://localhost:23004")
    args = ap.parse_args()

    progress = load_progress()
    lg = bs.login()
    if lg.error_code != "0":
        print(f"login failed: {lg.error_msg}")
        sys.exit(1)
    version = int(time.time() * 1_000_000)
    try:
        codes = [c.strip() for c in args.codes.split(",") if c.strip()]
        for i, code in enumerate(codes):
            done_until = progress.get(code, {}).get("done_until")
            seg_start = max(args.start, done_until) if done_until else args.start
            total = 0
            for y0, y1 in year_ranges(seg_start, args.end):
                bars, last_date = fetch_segment(code, y0, y1)
                if bars:
                    rows = []
                    for b in bars:
                        b = dict(b)
                        b["event_id"] = f"{b['market']}:{b['symbol']}:{b['period_min']}:{b['begin_ts']}"
                        b["ingest_version"] = version
                        rows.append(json.dumps(b, separators=(",", ":")))
                    local_ck_insert(args.ck, SQL, "\n".join(rows) + "\n")
                    total += len(bars)
                if last_date:
                    progress[code] = {"done_until": last_date}
                    save_progress(progress)
                time.sleep(args.gap)  # 分段间限速
            print(f"{code}: +{total} bars (done_until={progress.get(code, {}).get('done_until')})")
    finally:
        bs.logout()


if __name__ == "__main__":
    main()
