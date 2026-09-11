"""ai-query 单测：白名单条件模型、JSON 提取容错、NL2Condition 用例编排（mock 上游）。B16-A2 async 化。"""
from __future__ import annotations

import asyncio
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))  # ai-query/ 进入 path

import pytest

from app.application.nl2cond import AIQueryService, UnknownCondition
from app.domain.condition import (
    ConditionGroup, PctChange, PriceAbove, PriceBelow, PriceRange, cache_key,
    parse_condition, to_json, _extract_json,
)
from app.infrastructure.clickhouse import _cond_fragment, _hit_reason


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


# ---- v2（ADR-042）：PCT_CHANGE 规范形 / 公共字段 / 组合树 / SQL 片段 ----

def test_pct_change_signed_normalization():
    # 负值 ≡ direction=down 别名，归一化为正幅值
    assert parse_condition('{"type":"PCT_CHANGE","threshold":-5}') == PctChange(threshold=5, direction="down")
    # 显式 direction 保留
    assert parse_condition('{"type":"PCT_CHANGE","threshold":5,"direction":"up"}') == PctChange(
        threshold=5, direction="up")
    # 未指定 direction → None（执行端默认 both），dump 省略
    assert to_json(parse_condition('{"type":"PCT_CHANGE","threshold":3}')) == '{"type":"PCT_CHANGE","threshold":3.0}'


def test_pct_change_rejects_conflicts_and_bounds():
    for bad in [
        '{"type":"PCT_CHANGE","threshold":-5,"direction":"up"}',  # 负阈值与 up 冲突
        '{"type":"PCT_CHANGE","threshold":0}',  # 零阈值
        '{"type":"PCT_CHANGE","threshold":101}',  # 越界
        '{"type":"PCT_CHANGE","threshold":5,"limit":true}',  # limit 缺 direction
        '{"type":"PCT_CHANGE","threshold":5,"limit":true,"direction":"both"}',  # Flink limit 无 both
    ]:
        with pytest.raises(ValueError):
            parse_condition(bad)


def test_overlay_fields_carried_and_dumped_only_when_set():
    cond = parse_condition(
        '{"type":"PRICE_ABOVE","threshold":12.5,"once":true,"session":"open","expire_at":1730000000000}')
    assert cond.once is True and cond.session == "open" and cond.expire_at == 1730000000000
    assert '"once":true' in to_json(cond).replace(" ", "")
    # 缺省时不出现在 JSON（兼容 v1 形态）
    plain = parse_condition('{"type":"PRICE_ABOVE","threshold":12.5}')
    assert to_json(plain) == '{"type":"PRICE_ABOVE","threshold":12.5}'


def test_group_parse_and_depth_limit():
    g = parse_condition(
        '{"all":[{"type":"PCT_CHANGE","threshold":5,"direction":"up"},'
        '{"type":"PRICE_ABOVE","threshold":10}]}')
    assert isinstance(g, ConditionGroup) and len(g.all) == 2  # type: ignore[union-attr]
    # 嵌套一层（总深度 2）合法
    nested = parse_condition(
        '{"any":[{"type":"VOLUME_ABOVE","threshold":1e6},'
        '{"all":[{"type":"PRICE_ABOVE","threshold":1},{"type":"PRICE_BELOW","threshold":2}]}]}')
    assert isinstance(nested, ConditionGroup) and nested.any is not None  # type: ignore[union-attr]
    # 深度 3 / 空组 / 双连接词 / 超 10 项 / 同时带 type → 拒绝
    depth3 = '{"all":[{"all":[{"all":[{"type":"PRICE_ABOVE","threshold":1}]}]}]}'
    for bad in [depth3, '{"all":[]}', '{"all":[],"any":[]}',
                '{"all":[' + ','.join(['{"type":"PRICE_ABOVE","threshold":1}'] * 11) + ']}',
                '{"type":"PRICE_ABOVE","threshold":1,"all":[]}']:
        with pytest.raises(ValueError):
            parse_condition(bad)


def test_sql_fragment_direction_and_unique_params():
    import itertools

    def fresh():
        return {}, itertools.count()

    params, seq = fresh()
    up = _cond_fragment(parse_condition('{"type":"PCT_CHANGE","threshold":5,"direction":"up"}'),
                        params, seq)
    assert ">= {p0:Float64}" in up and "abs(" not in up
    params, seq = fresh()
    down = _cond_fragment(parse_condition('{"type":"PCT_CHANGE","threshold":-5}'), params, seq)
    assert "<= -{p0:Float64}" in down
    # 组合树：参数名唯一（两个叶子不共用 p0），括号成对
    params, seq = fresh()
    group = _cond_fragment(parse_condition(
        '{"all":[{"type":"PRICE_ABOVE","threshold":10},{"type":"PRICE_BELOW","threshold":50}]}'),
        params, seq)
    assert group.startswith("(") and group.endswith(")") and " AND " in group
    assert sorted(k for k in params if k != "mkt") == ["p0", "p1"]


def test_hit_reason_group_joins():
    g = parse_condition(
        '{"all":[{"type":"PRICE_ABOVE","threshold":10},{"type":"PRICE_BELOW","threshold":50}]}')
    reason = _hit_reason(g, {"last": 20, "volume": 1})
    assert " AND " in reason and "last 20 > 10.0" in reason


# ---- 共享 fixture（ADR-042 单一事实源，Java/Vitest 消费同一文件） ----

def _load_fixture() -> dict:
    import json
    fixture = Path(__file__).resolve().parents[3] / "tests" / "fixtures" / "conditions.v2.json"
    return json.loads(fixture.read_text(encoding="utf-8"))


def test_shared_fixture_valid_query_cases():
    import json
    for case in _load_fixture()["valid_query"]:
        try:
            cond = parse_condition(case["input"])
            assert json.loads(to_json(cond)) == case["canonical"], f"canonical mismatch: {case['name']}"
        except AssertionError:
            raise
        except Exception as exc:  # noqa: BLE001 合法用例任何异常都归属到具体 case
            raise AssertionError(f"valid case rejected: {case['name']} ({exc})") from exc


def test_shared_fixture_invalid_query_cases():
    for case in _load_fixture()["invalid_query"]:
        try:
            parse_condition(case["input"])
        except ValueError:
            continue
        except Exception as exc:  # noqa: BLE001 非 ValueError 的异常类型也算护栏失效
            raise AssertionError(f"wrong error type: {case['name']} ({exc!r})") from exc
        raise AssertionError(f"must reject: {case['name']}")


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
