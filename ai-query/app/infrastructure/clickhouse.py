"""ClickHouse 适配器：最新快照筛选（U0 直读 CK；Doris 启用后此模块替换为 Doris 查询，docs/09 §5.1）。

安全口径：
- 条件类型来自 domain 白名单枚举 → 分支内固定 SQL 片段（非用户文本拼接）；
- 数值一律 {name:Type} 参数占位下发（与 /quote/kline、脚本族同口径）；
- 查询仅读 tick_local。
- 排序/命中原因（Branch 15）：结果按 last_price 降序，附命中原因（命中的条件片段描述）。
"""
from __future__ import annotations

import json
import urllib.parse
import urllib.request

import httpx

from ..domain.condition import (
    Condition, PriceAbove, PriceBelow, PriceRange, PctChange, VolumeAbove,
)


def _cond_fragment(cond: Condition, params: dict[str, str]) -> str:
    """类型 → 固定 HAVING 片段；数值经参数占位绑定。"""
    if isinstance(cond, PriceAbove):
        params["t"] = repr(cond.threshold)
        return "last_price > {t:Float64}"
    if isinstance(cond, PriceBelow):
        params["t"] = repr(cond.threshold)
        return "last_price < {t:Float64}"
    if isinstance(cond, PriceRange):
        params["lo"] = repr(cond.low)
        params["hi"] = repr(cond.high)
        return "last_price >= {lo:Float64} AND last_price <= {hi:Float64}"
    if isinstance(cond, PctChange):
        params["t"] = repr(cond.threshold)
        return "pre_close > 0 AND abs((last_price - pre_close) / pre_close * 100.0) > {t:Float64}"
    if isinstance(cond, VolumeAbove):
        params["t"] = repr(cond.threshold)
        return "volume > {t:Float64}"
    raise ValueError("unreachable")  # pragma: no cover


def _hit_reason(cond: Condition, row: dict) -> str:
    """命中原因（可解释性，docs/10 §11 选股演进第 3 步）。"""
    t = cond.type if hasattr(cond, "type") else ""
    if t == "PRICE_ABOVE":
        return f"last {row['last']} > {cond.threshold}"
    if t == "PRICE_BELOW":
        return f"last {row['last']} < {cond.threshold}"
    if t == "PCT_CHANGE":
        return f"|pct| >= {cond.threshold}%"
    if t == "VOLUME_ABOVE":
        return f"volume {row['volume']:.0f} > {cond.threshold:.0f}"
    if t == "PRICE_RANGE":
        return f"{cond.low} <= {row['last']} <= {cond.high}"
    return ""


class ClickHouseReader:
    def __init__(self, base_url: str | None = None, user: str = "hqpush",
                 password: str = "hqpush", timeout: float = 8.0) -> None:
        import os
        self.base = (base_url or os.environ.get("HQ_CLICKHOUSE_URL", "http://127.0.0.1:23004")).rstrip("/")
        self.user = user or os.environ.get("HQ_CLICKHOUSE_USER", "hqpush")
        self.password = password or os.environ.get("HQ_CLICKHOUSE_PASSWORD", "hqpush")
        self._http = httpx.Client(timeout=timeout)

    def screen(self, cond: Condition, market: str = "A_SHARE", limit: int = 50) -> list[dict]:
        params: dict[str, str] = {"mkt": market, "lim": str(min(max(limit, 1), 200))}
        having = _cond_fragment(cond, params)
        sql = f"""
SELECT symbol,
       argMax(last_price, ts_ms) AS last_price,
       argMax(pre_close, ts_ms)  AS pre_close,
       argMax(volume, ts_ms)     AS volume
FROM tick_local
WHERE market = {{mkt:String}}
GROUP BY symbol
HAVING {having}
ORDER BY last_price DESC
LIMIT {{lim:UInt32}}
FORMAT JSON
"""
        qs = "query=" + urllib.parse.quote(sql)
        for k, v in params.items():
            qs += f"&param_{k}=" + urllib.parse.quote(v)
        resp = self._http.get(
            self.base + "/?" + qs,
            headers={"Authorization": _basic(self.user, self.password)},
        )
        if resp.status_code != 200:
            raise RuntimeError(f"clickhouse {resp.status_code}: {resp.text[:200]}")
        return [
            {
                "symbol": r["symbol"],
                "last": float(r["last_price"]),
                "pct": round((float(r["last_price"]) - float(r["pre_close"]))
                             / float(r["pre_close"]) * 100, 2) if float(r["pre_close"]) > 0 else 0.0,
                "volume": float(r["volume"]),
            }
            for r in resp.json().get("data", [])
        ]


def _basic(user: str, password: str) -> str:
    import base64
    token = base64.b64encode(f"{user}:{password}".encode()).decode()
    return f"Basic {token}"
