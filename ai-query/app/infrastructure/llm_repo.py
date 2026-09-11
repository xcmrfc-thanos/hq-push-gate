"""llm_provider/llm_model 渠道仓库（aiomysql；docs/12 B16-A2）。

- list_channels(type)：JOIN 两表取 enabled 渠道，解密 api_key（v1:<kid>: 密文）；
- 进程内 30s TTL 缓存（按 model_type 分桶），admin 写库后 invalidate() 即时生效；
- 回退链：MySQL 无行/不可达/密钥未配 → env 单渠道（LLM_*，兼容现网）→ 空列表。
安全口径：全部参数化查询（%s 占位，禁拼接）；base_url 读取时二次过 SSRF 校验。
"""
from __future__ import annotations

import asyncio
import json
import time
from typing import Any

import aiomysql

from .llm import DEFAULT_USER_AGENT, Channel, channel_from_env, load_env
from .secrets import SecretError, decrypt, validate_base_url

CACHE_TTL_SECONDS = 30.0


class ChannelRepo:
    def __init__(self, pool: aiomysql.Pool | None = None, env: dict[str, str] | None = None,
                 ttl: float = CACHE_TTL_SECONDS) -> None:
        self._pool = pool
        self._env = env if env is not None else load_env()
        self._ttl = ttl
        self._cache: dict[str, tuple[float, list[Channel]]] = {}
        self._lock = asyncio.Lock()

    def invalidate(self) -> None:
        """admin 写库后调用：下一次 list_channels 强制回源。"""
        self._cache.clear()

    async def list_channels(self, model_type: str = "chat") -> list[Channel]:
        async with self._lock:
            now = time.monotonic()
            hit = self._cache.get(model_type)
            if hit and now - hit[0] < self._ttl:
                return hit[1]
            channels = await self._fetch(model_type)
            self._cache[model_type] = (now, channels)
            return channels

    async def _fetch(self, model_type: str) -> list[Channel]:
        if self._pool is None:
            return self._env_fallback()
        try:
            async with self._pool.acquire() as conn:
                async with conn.cursor(aiomysql.DictCursor) as cur:
                    await cur.execute(
                        """
SELECT m.id AS model_db_id, p.name AS provider_name, p.base_url,
       p.api_key_enc, p.extra, m.model_id, m.weight, m.max_tokens
FROM llm_model m
JOIN llm_provider p ON p.id = m.provider_id
WHERE m.enabled = 1 AND p.enabled = 1 AND m.model_type = %s
ORDER BY m.weight DESC, m.id
""",
                        (model_type,),
                    )
                    rows: list[dict[str, Any]] = list(await cur.fetchall())
        except Exception:  # noqa: BLE001 MySQL 不可达/表未建 → env 回退，服务不中断
            return self._env_fallback()
        if not rows:
            return self._env_fallback()
        out: list[Channel] = []
        for r in rows:
            try:
                out.append(Channel(
                    model_db_id=int(r["model_db_id"]),
                    name=f"{r['provider_name']}/{r['model_id']}",
                    base_url=validate_base_url(str(r["base_url"]), allow_private=True),
                    api_key=decrypt(str(r["api_key_enc"])),
                    model_id=str(r["model_id"]),
                    weight=max(1, int(r["weight"] or 1)),
                    max_tokens_cap=max(64, int(r["max_tokens"] or 2048)),
                    **_extra_fields(r["extra"], str(r["provider_name"])),
                ))
            except (SecretError, ValueError, TypeError):
                continue  # 密钥未配/密文坏/base_url 不合规 → 跳过该渠道
        return out or self._env_fallback()

    def _env_fallback(self) -> list[Channel]:
        ch = channel_from_env(self._env)
        return [ch] if ch else []


def _extra_fields(extra: Any, provider_name: str) -> dict:
    """llm_provider.extra JSON → Channel 差异化字段（user_agent；thinking 仅 ark 生效）。"""
    fields: dict = {}
    if not extra:
        return fields
    try:
        data = json.loads(extra) if isinstance(extra, str) else extra
    except ValueError:
        return fields
    if not isinstance(data, dict):
        return fields
    if data.get("user_agent"):
        fields["user_agent"] = str(data["user_agent"])
    elif provider_name != "ark":
        fields["user_agent"] = DEFAULT_USER_AGENT
    if provider_name == "ark" and "thinking" in data:
        fields["thinking"] = bool(data["thinking"])
    return fields
