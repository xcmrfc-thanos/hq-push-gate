"""Redis 适配器：aicond:{hash} 条件/结果缓存 30min（docs/04 §5）。redis.asyncio（B16-A2 async 化）。

注意：写入值只含已通过白名单校验的结构化数据，不含用户原文（避免缓存注入面）。
"""
from __future__ import annotations

import json
import os

import redis.asyncio as aioredis

TTL_SECONDS = 1800


class CondCache:
    def __init__(self, url: str | None = None) -> None:
        url = url or os.environ.get("HQ_REDIS_URL", "redis://127.0.0.1:23002/0")
        self.r = aioredis.from_url(url, socket_timeout=2, decode_responses=True)

    async def get(self, key: str) -> dict | None:
        try:
            v = await self.r.get(key)
        except aioredis.RedisError:
            return None  # 缓存不可用降级直查
        if not v:
            return None
        try:
            return json.loads(v)
        except json.JSONDecodeError:
            return None

    async def put(self, key: str, payload: dict) -> None:
        try:
            await self.r.set(key, json.dumps(payload, ensure_ascii=False), ex=TTL_SECONDS)
        except aioredis.RedisError:
            pass  # 缓存失败不影响主流程

    async def aclose(self) -> None:
        try:
            await self.r.aclose()
        except aioredis.RedisError:
            pass
