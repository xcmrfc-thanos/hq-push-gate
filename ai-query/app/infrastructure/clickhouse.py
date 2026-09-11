"""ClickHouse 适配器：最新快照筛选（U0 直读 CK；Doris 启用后此模块替换为 Doris 查询，docs/09 §5.1）。

安全口径：
- 条件类型来自 domain 白名单枚举 → 分支内固定 SQL 片段（非用户文本拼接）；
- 数值一律 {name:Type} 参数占位下发（与 /quote/kline、脚本族同口径）；组合树下参数名唯一化；
- 查询仅读 tick_local。
- 排序/命中原因（Branch 15）：结果按 last_price 降序，附命中原因（命中的条件片段描述）。
- v2（ADR-042）：PctChange 按 direction 分支（up: pct>=t / down: pct<=-t / both: |pct|>=t），
  边界语义与 Flink matchPctChange 对齐；ConditionGroup(all/any) 生成嵌套括号片段。
"""
from __future__ import annotations

import urllib.parse
from itertools import count

import httpx

from ..domain.condition import (
    ConditionGroup, ConditionNode, PriceAbove, PriceBelow, PriceRange, PctChange, VolumeAbove,
)


def _cond_fragment(cond: ConditionNode, params: dict[str, str], seq: count) -> str:
    """条件 → 固定 HAVING 片段；数值经参数占位绑定（组合树参数名经 seq 唯一化）。"""
    if isinstance(cond, ConditionGroup):
        children = cond.all if cond.all is not None else cond.any
        joiner = " AND " if cond.all is not None else " OR "
        return "(" + joiner.join(
            _cond_fragment(child, params, seq) for child in (children or [])) + ")"
    if isinstance(cond, PriceAbove):
        return _bind(params, seq, "last_price > {p:Float64}", cond.threshold)
    if isinstance(cond, PriceBelow):
        return _bind(params, seq, "last_price < {p:Float64}", cond.threshold)
    if isinstance(cond, PriceRange):
        name = f"p{next(seq)}"
        params[name + "lo"] = repr(cond.low)
        params[name + "hi"] = repr(cond.high)
        return f"last_price >= {{{name}lo:Float64}} AND last_price <= {{{name}hi:Float64}}"
    if isinstance(cond, PctChange):
        pct = "(last_price - pre_close) / pre_close * 100.0"
        if cond.direction == "up":
            return _bind(params, seq, f"pre_close > 0 AND {pct} >= {{p:Float64}}", cond.threshold)
        if cond.direction == "down":
            return _bind(params, seq, f"pre_close > 0 AND {pct} <= -{{p:Float64}}", cond.threshold)
        return _bind(params, seq, f"pre_close > 0 AND abs({pct}) >= {{p:Float64}}", cond.threshold)
    if isinstance(cond, VolumeAbove):
        return _bind(params, seq, "volume > {p:Float64}", cond.threshold)
    raise ValueError("unreachable")  # pragma: no cover


def _bind(params: dict[str, str], seq: count, template: str, value: float) -> str:
    """参数名取自 seq 保证组合树内唯一；值为 repr(float)，只进参数位不进 SQL 文本。"""
    name = f"p{next(seq)}"
    params[name] = repr(value)
    return template.replace("{p:", "{" + name + ":")


def _hit_reason(cond: ConditionNode, row: dict) -> str:
    """命中原因（可解释性，docs/10 §11 选股演进第 3 步）；组合树按连接词拼接子原因。"""
    if isinstance(cond, ConditionGroup):
        children = cond.all if cond.all is not None else cond.any
        joiner = " AND " if cond.all is not None else " OR "
        return joiner.join(_hit_reason(child, row) for child in (children or []))
    t = cond.type
    if t == "PRICE_ABOVE":
        return f"last {row['last']} > {cond.threshold}"
    if t == "PRICE_BELOW":
        return f"last {row['last']} < {cond.threshold}"
    if t == "PCT_CHANGE":
        arrow = ">=" if cond.direction in ("up", "both") else "<="
        sign = "" if cond.direction != "down" else "-"
        return f"pct {sign}{arrow} {cond.threshold}%"
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

    def screen(self, cond: ConditionNode, market: str = "A_SHARE", limit: int = 50) -> list[dict]:
        params: dict[str, str] = {"mkt": market, "lim": str(min(max(limit, 1), 200))}
        having = _cond_fragment(cond, params, count())
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
