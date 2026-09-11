"""NL2Condition 用例（application 层）：LLM 解析 → 白名单校验 → 缓存 → CK 筛选。B16-A2 async 化。"""
from __future__ import annotations

import asyncio
import json

from ..domain.condition import Condition, cache_key, parse_condition, to_json
from ..infrastructure.clickhouse import ClickHouseReader
from ..infrastructure.fewshot import FEW_SHOTS
from ..infrastructure.llm_gateway import LLMGateway
from ..infrastructure.redis_cache import CondCache

SYSTEM_PROMPT = """你是股票行情筛选条件解析器。把用户的自然语言转换为以下五种 JSON 条件之一，只输出 JSON，不要输出任何解释或代码块：

股价大于/高于/超过 X 元 -> {"type":"PRICE_ABOVE","threshold":X}
股价小于/低于 X 元 -> {"type":"PRICE_BELOW","threshold":X}
股价在 X 到 Y 元之间 -> {"type":"PRICE_RANGE","low":X,"high":Y}
涨跌幅超过 X%（涨或跌都算）-> {"type":"PCT_CHANGE","threshold":X}
只要涨的 -> {"type":"PCT_CHANGE","threshold":X,"direction":"up"}
只要跌的 -> {"type":"PCT_CHANGE","threshold":X,"direction":"down"}
成交量大于 X 股（手=100股 需换算）-> {"type":"VOLUME_ABOVE","threshold":X}

组合条件：多个条件同时满足 -> {"all":[条件1,条件2]}；任一满足 -> {"any":[条件1,条件2]}（嵌套最多2层，单组最多10个条件）。

要求：无法对应以上任何一种（如提到指标、新闻、模糊描述）时输出 {"type":"UNKNOWN"}。
数值用阿拉伯数字，不带单位。只输出一个 JSON 对象。"""

# few-shot 示例（Branch 15 定义、B20 接线）：提升解析准确率，防提示词改动回归
SYSTEM_PROMPT = SYSTEM_PROMPT + "\n\n" + FEW_SHOTS


class UnknownCondition(ValueError):
    """LLM 无法解析为白名单条件。"""


class AIQueryService:
    def __init__(self, llm: LLMGateway, ck: ClickHouseReader, cache: CondCache) -> None:
        self.llm = llm
        self.ck = ck
        self.cache = cache

    async def query(self, question: str, limit: int = 50, client_ip: str | None = None) -> dict:
        q = question.strip()
        if not q or len(q) > 500:
            raise ValueError("question required (1..500 chars)")

        key = cache_key(q)
        if (hit := await self.cache.get(key)) is not None:
            return {**hit, "cached": True}

        cond = await self._parse(q, client_ip)
        rows = await asyncio.to_thread(self.ck.screen, cond, limit=limit)
        payload = {"condition": json.loads(to_json(cond)), "symbols": rows}
        await self.cache.put(key, payload)
        return {**payload, "cached": False}

    async def _parse(self, question: str, client_ip: str | None = None) -> Condition:
        text, _channel = await self.llm.complete(SYSTEM_PROMPT, question, client_ip=client_ip)
        try:
            return parse_condition(text)
        except ValueError as exc:
            raise UnknownCondition(str(exc)) from exc
