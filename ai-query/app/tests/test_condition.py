"""ai-query 单测：白名单条件模型、JSON 提取容错、NL2Condition 用例编排（mock 上游）。B16-A2 async 化。"""
from __future__ import annotations

import asyncio
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))  # ai-query/ 进入 path

import pytest

from app.application.nl2cond import AIQueryService, UnknownCondition
from app.domain.condition import (
    PriceAbove, PriceBelow, PriceRange, cache_key, parse_condition, to_json, _extract_json,
)


def _run(coro):
    return asyncio.run(coro)


# ---- 白名单条件模型 ----

def test_parse_valid_conditions():
    assert parse_condition('{"type":"PRICE_ABOVE","threshold":12.5}') == PriceAbove(threshold=12.5)
    assert parse_condition({"type": "PRICE_BELOW", "threshold": 5}) == PriceBelow(threshold=5)
    assert parse_condition('{"type":"PRICE_RANGE","low":10,"high":20}') == PriceRange(low=10, high=20)


def test_parse_rejects_outside_whitelist():
    for bad in [
        '{"type":"INDICATOR","indicator":"RSI","op":">","value":80}',  # 冻结枚举但 U0 不支持
        '{"type":"UNKNOWN"}',
        '{"type":"PRICE_ABOVE"}',  # 缺 threshold
        '{"type":"PRICE_ABOVE","threshold":-1}',  # 越界
        '{"type":"PRICE_RANGE","low":20,"high":10}',  # low>=high
        '{"type":"EVIL","threshold":"1; DROP TABLE tick_local"}',
        'not json at all',
        '{"type":"PRICE_ABOVE","threshold":NaN}',  # 非法数值
    ]:
        with pytest.raises(ValueError):
            parse_condition(bad)


def test_extract_json_tolerates_fences_and_prose():
    assert _extract_json('前置说明```json\n{"type":"UNKNOWN"}\n```后缀') == {"type": "UNKNOWN"}
    assert _extract_json('结果是 {"type":"PRICE_ABOVE","threshold":3.5} 请查收') == {
        "type": "PRICE_ABOVE", "threshold": 3.5,
    }


def test_cache_key_stable():
    assert cache_key("股价大于10元") == cache_key(" 股价大于10元 ")
    assert cache_key("a") != cache_key("b")


# ---- 用例编排（mock 上游） ----

class FakeLLM:
    def __init__(self, reply: str) -> None:
        self.reply = reply
        self.prompts: list[str] = []

    async def complete(self, system: str, user: str, max_tokens: int = 300, client_ip: str | None = None):
        self.prompts.append(user)
        return self.reply, "fake"


class FakeCK:
    def __init__(self, rows: list[dict] | None = None) -> None:
        self.rows = rows or []
        self.last_cond = None

    def screen(self, cond, market="A_SHARE", limit=50):
        self.last_cond = cond
        return self.rows


class FakeCache:
    def __init__(self) -> None:
        self.data: dict = {}

    async def get(self, key):
        return self.data.get(key)

    async def put(self, key, payload):
        self.data[key] = payload


def test_query_happy_path_and_cache():
    llm = FakeLLM('{"type":"PRICE_ABOVE","threshold":100}')
    ck = FakeCK([{"symbol": "600519", "last": 1330.0, "pct": 2.4, "volume": 1e6}])
    cache = FakeCache()
    svc = AIQueryService(llm, ck, cache)

    out = _run(svc.query("价格超过100元的股票", 10))
    assert out["cached"] is False
    assert out["condition"]["type"] == "PRICE_ABOVE"
    assert out["symbols"][0]["symbol"] == "600519"
    # 二次同问命中缓存，不再调用 LLM/CK
    out2 = _run(svc.query("价格超过100元的股票", 10))
    assert out2["cached"] is True and out2["symbols"] == out["symbols"]
    assert len(llm.prompts) == 1


def test_query_unknown_condition_maps_to_error():
    svc = AIQueryService(FakeLLM('{"type":"UNKNOWN"}'), FakeCK(), FakeCache())
    with pytest.raises(UnknownCondition):
        _run(svc.query("今天天气怎么样"))


def test_query_strips_injection_suffix_and_keeps_whitelisted_cond():
    # LLM 被诱导附加 SQL 注入后缀 → 仅提取首个 JSON 对象；数值为纯 float，不携带注入载荷
    ck = FakeCK()
    svc = AIQueryService(
        FakeLLM('{"type":"PRICE_ABOVE","threshold":1} UNION SELECT 1 --'),
        ck, FakeCache(),
    )
    out = _run(svc.query("anything"))
    assert ck.last_cond.threshold == 1.0  # type: ignore[union-attr]
    assert out["condition"] == {"type": "PRICE_ABOVE", "threshold": 1.0}


def test_to_json_roundtrip():
    assert to_json(PriceAbove(threshold=12.5)) == '{"type":"PRICE_ABOVE","threshold":12.5}'
