#!/usr/bin/env python3
"""全市场 A 股列表 → MySQL symbol_meta（V2：名称/板块/上市日期静态属性，docs/09 §5.1）。

数据源：东财 clist 免费接口（AKShare stock_zh_a_spot_em 同源）。
SQL 全参数绑定（execute %s 占位符），幂等 upsert（8.0.19+ 行别名语法）。

用法：
  py -3 scripts/backfill/sync_symbol_meta.py
环境：HQ_MYSQL_HOST/PORT/USER/PASSWORD/DB（默认 localhost:23001 hqpush/hqpush/hqpush）
"""
import argparse
import os
import time
import urllib.parse

import pymysql

from netguard import external_get_json_multi

ALLOWED_HOSTS = {"push2.eastmoney.com", "push2delay.eastmoney.com"}
HOSTS = ["push2.eastmoney.com", "push2delay.eastmoney.com"]
# 沪主板+科创板 / 深主板+创业板（不含北交所，市场口径与 tick 链路一致）
FS = "m:0+t:6,m:0+t:80,m:1+t:2,m:1+t:23"
MIN_LIST_TS = 631152000  # 1990-01-01：过滤接口返回的占位小时间戳

UPSERT = """
INSERT INTO symbol_meta (market, symbol, name, board, list_date, status)
VALUES (%s, %s, %s, %s, %s, %s) AS new
ON DUPLICATE KEY UPDATE name=new.name, board=new.board,
list_date=new.list_date, status=new.status
"""


def fetch_page(page: int, size: int) -> dict:
    qs = urllib.parse.urlencode({
        "pn": page, "pz": size, "po": 1, "np": 1,
        "fltt": 2, "inav": 1, "fid": "f12", "fs": FS,
        "fields": "f12,f13,f14,f26,f100",
    })
    urls = [f"https://{h}/api/qt/clist/get?{qs}" for h in HOSTS]
    return external_get_json_multi(urls, ALLOWED_HOSTS)


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--page-size", type=int, default=200)
    ap.add_argument("--gap", type=float, default=0.5)
    args = ap.parse_args()

    conn = pymysql.connect(
        host=os.environ.get("HQ_MYSQL_HOST", "localhost"),
        port=int(os.environ.get("HQ_MYSQL_PORT", "23001")),
        user=os.environ.get("HQ_MYSQL_USER", "hqpush"),
        password=os.environ.get("HQ_MYSQL_PASSWORD", "hqpush"),
        database=os.environ.get("HQ_MYSQL_DB", "hqpush"),
        charset="utf8mb4",
    )
    total, page = 0, 1
    try:
        with conn.cursor() as cur:
            while True:
                data = fetch_page(page, args.page_size).get("data") or {}
                diff = data.get("diff") or []
                if not diff:
                    break
                for d in diff:
                    code = str(d.get("f12", ""))
                    if len(code) != 6:
                        continue
                    list_ts = d.get("f26")
                    list_date = (time.strftime("%Y-%m-%d", time.localtime(list_ts))
                                 if isinstance(list_ts, (int, float)) and list_ts > MIN_LIST_TS
                                 else None)
                    cur.execute(UPSERT, (
                        "A_SHARE", code, str(d.get("f14", "")),
                        d.get("f100") or None, list_date, 1,
                    ))
                conn.commit()
                total += len(diff)
                got = data.get("total") or 0
                if page * args.page_size >= got:
                    break
                page += 1
                time.sleep(args.gap)
        print(f"symbol_meta upserted: {total}")
    finally:
        conn.close()


if __name__ == "__main__":
    main()
