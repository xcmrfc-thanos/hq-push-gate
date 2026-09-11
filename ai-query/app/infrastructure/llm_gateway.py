"""LLM 网关（docs/12 B16-A2）：平滑加权轮询 + 冷却熔断 + 日配额 + 并发帽 + TLS 预热。

熔断口径（状态存 Redis 跨副本共享，键 llm:ch:{id}:fail / llm:ch:{id}:cooldown；Redis 不可用降级进程内）：
- 连续失败 ≥5 → 冷却 60s×2^n（封顶 600s），冷却 TTL 过期即半开放行；
- 401/403（坏凭据）直接冷却 300s；429 冷却 60s；成功清零。
日配额（防刷量，缺陷 #22）：llm:quota:global:{date} 与 llm:quota:ip:{ip}:{date}，CST 午夜过期；
per-user 配额待 B17 统一转发 uid 后升级（docs/12 §1.1）。
"""
from __future__ import annotations

import asyncio
import datetime as _dt

import httpx
from prometheus_client import Counter, Histogram

from .llm import Channel, LLMError, complete_once

CST = _dt.timezone(_dt.timedelta(hours=8))

FAIL_THRESHOLD = 5
COOLDOWN_BASE_S = 60
COOLDOWN_MAX_S = 600
AUTH_COOLDOWN_S = 300      # 401/403 坏凭据：短期重试无意义
RATE_COOLDOWN_S = 60       # 429：歇一分钟再回来
MAX_ATTEMPTS = 3           # 单请求最多试 3 个不同渠道

_REQS = Counter("llm_upstream_requests_total", "LLM 渠道请求结果数", ["channel", "result"])
_LAT = Histogram("llm_upstream_seconds", "LLM 渠道请求耗时秒", ["channel"],
                 buckets=(0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30))


class GatewayConfig:
    def __init__(self, max_attempts: int = MAX_ATTEMPTS, max_concurrency: int = 10,
                 fail_threshold: int = FAIL_THRESHOLD,
                 daily_global: int = 2000, daily_per_ip: int = 50,
                 prewarm_interval: float = 60.0) -> None:
        self.max_attempts = max_attempts
        self.max_concurrency = max_concurrency
        self.fail_threshold = fail_threshold
        self.daily_global = daily_global
        self.daily_per_ip = daily_per_ip
        self.prewarm_interval = prewarm_interval


def _cst_midnight_ttl() -> int:
    now = _dt.datetime.now(CST)
    nxt = (now + _dt.timedelta(days=1)).replace(hour=0, minute=0, second=0, microsecond=0)
    return max(1, int((nxt - now).total_seconds()))


class ChannelState:
    """失败计数与冷却：Redis SETEX/INCR（跨副本一致），Redis 不可用降级进程内 dict。"""

    def __init__(self, rdb=None) -> None:
        self._rdb = rdb
        self._mem: dict[str, tuple[int, float]] = {}  # key -> (value, 过期 epoch)；value=0 表冷却标记

    async def _incr(self, key: str, ttl: int = 0) -> int:
        if self._rdb is not None:
            try:
                n = int(await self._rdb.incr(key))
                if ttl:
                    await self._rdb.expire(key, ttl)
                return n
            except Exception:  # noqa: BLE001 Redis 降级
                pass
        val, exp = self._mem.get(key, (0, 0.0))
        import time as _t
        if exp and _t.monotonic() > exp:
            val = 0
        self._mem[key] = (val + 1, exp)
        return val + 1

    async def fail_count_incr(self, ch_id: int) -> int:
        return await self._incr(f"llm:ch:{ch_id}:fail")

    async def cooldown(self, ch_id: int, seconds: int) -> None:
        key = f"llm:ch:{ch_id}:cooldown"
        if self._rdb is not None:
            try:
                await self._rdb.set(key, "1", ex=max(1, int(seconds)))
                return
            except Exception:  # noqa: BLE001
                pass
        import time as _t
        self._mem[key] = (1, _t.monotonic() + max(0.001, seconds))

    async def in_cooldown(self, ch_id: int) -> bool:
        key = f"llm:ch:{ch_id}:cooldown"
        if self._rdb is not None:
            try:
                return bool(await self._rdb.exists(key))
            except Exception:  # noqa: BLE001
                pass
        import time as _t
        val, exp = self._mem.get(key, (0, 0.0))
        return val > 0 and _t.monotonic() <= exp

    async def reset(self, ch_id: int) -> None:
        for key in (f"llm:ch:{ch_id}:fail", f"llm:ch:{ch_id}:cooldown"):
            if self._rdb is not None:
                try:
                    await self._rdb.delete(key)
                except Exception:  # noqa: BLE001
                    pass
            self._mem.pop(key, None)


class Quota:
    """日配额计数（0=不限）：先到先得，超限抛 LLMError(status=429, retry_after=距 CST 午夜秒)。"""

    def __init__(self, rdb=None, daily_global: int = 0, daily_per_ip: int = 0) -> None:
        self._rdb = rdb
        self.daily_global = daily_global
        self.daily_per_ip = daily_per_ip
        self._mem: dict[str, int] = {}

    async def check_and_incr(self, client_ip: str | None) -> None:
        date = _dt.datetime.now(CST).strftime("%Y%m%d")
        ttl = _cst_midnight_ttl()
        if self.daily_global > 0:
            n = await self._incr(f"llm:quota:global:{date}", ttl)
            if n > self.daily_global:
                raise LLMError("llm daily quota exceeded", status=429, retry_after=ttl)
        if self.daily_per_ip > 0 and client_ip:
            n = await self._incr(f"llm:quota:ip:{client_ip}:{date}", ttl)
            if n > self.daily_per_ip:
                raise LLMError("llm ip daily quota exceeded", status=429, retry_after=ttl)

    async def _incr(self, key: str, ttl: int) -> int:
        if self._rdb is not None:
            try:
                n = int(await self._rdb.incr(key))
                await self._rdb.expire(key, ttl)
                return n
            except Exception:  # noqa: BLE001 Redis 降级
                pass
        self._mem[key] = self._mem.get(key, 0) + 1
        return self._mem[key]


class LLMGateway:
    def __init__(self, repo, http: httpx.AsyncClient, state: ChannelState | None = None,
                 quota: Quota | None = None, cfg: GatewayConfig | None = None) -> None:
        self._repo = repo
        self._http = http
        self._state = state or ChannelState(None)
        self._quota = quota or Quota(None, 0, 0)
        self._cfg = cfg or GatewayConfig()
        self._sem = asyncio.Semaphore(self._cfg.max_concurrency)
        self._cur_weight: dict[int, int] = {}  # 平滑加权轮询游标（nginx 算法）

    async def complete(self, system: str, user: str, max_tokens: int = 300,
                       client_ip: str | None = None) -> tuple[str, str]:
        """返回 (content, channel_name)；全部渠道失败抛最后一个 LLMError。"""
        await self._quota.check_and_incr(client_ip)
        channels = [
            c for c in await self._repo.list_channels("chat")
            if not await self._state.in_cooldown(c.model_db_id)
        ]
        if not channels:
            raise LLMError("no available llm channel")
        tried_ids: set[int] = set()
        last_err: LLMError | None = None
        for ch in self._order(channels):
            if len(tried_ids) >= self._cfg.max_attempts:
                break
            if ch.model_db_id in tried_ids:
                continue
            tried_ids.add(ch.model_db_id)
            try:
                async with self._sem:
                    import time as _t
                    t0 = _t.monotonic()
                    text = await complete_once(self._http, ch, system, user, max_tokens)
                    _LAT.labels(ch.name).observe(_t.monotonic() - t0)
                _REQS.labels(ch.name, "ok").inc()
                await self._state.reset(ch.model_db_id)
                return text, ch.name
            except LLMError as exc:
                _REQS.labels(ch.name, "fail").inc()
                last_err = exc
                await self._penalize(ch, exc)
        raise last_err or LLMError("all llm channels failed")

    def _order(self, channels: list[Channel]) -> list[Channel]:
        """平滑加权轮询选中队首，其余按权重降序作 fallback 序。"""
        if not channels:
            return channels
        total = sum(c.weight for c in channels)
        best = channels[0]
        for c in channels:
            self._cur_weight[c.model_db_id] = self._cur_weight.get(c.model_db_id, 0) + c.weight
            if self._cur_weight[c.model_db_id] > self._cur_weight[best.model_db_id]:
                best = c
        self._cur_weight[best.model_db_id] -= total
        rest = sorted((c for c in channels if c is not best), key=lambda c: (-c.weight, c.model_db_id))
        return [best, *rest]

    async def _penalize(self, ch: Channel, err: LLMError) -> None:
        if err.status in (401, 403):
            await self._state.cooldown(ch.model_db_id, AUTH_COOLDOWN_S)
            return
        if err.status == 429:
            await self._state.cooldown(ch.model_db_id, RATE_COOLDOWN_S)
            return
        n = await self._state.fail_count_incr(ch.model_db_id)
        if n >= self._cfg.fail_threshold:
            cd = min(COOLDOWN_BASE_S * (2 ** (n - self._cfg.fail_threshold)), COOLDOWN_MAX_S)
            await self._state.cooldown(ch.model_db_id, cd)

    async def prewarm(self) -> None:
        """GET {base_url}/models：提前完成 DNS/TCP/TLS 握手入连接池（TTFT 优化）。"""
        for ch in await self._repo.list_channels("chat"):
            try:
                await self._http.get(
                    f"{ch.base_url}/models",
                    headers={"Authorization": f"Bearer {ch.api_key}", "User-Agent": ch.user_agent},
                )
            except Exception:  # noqa: BLE001 预热失败不影响服务
                pass

    async def prewarm_loop(self, interval: float | None = None) -> None:
        while True:
            await self.prewarm()
            await asyncio.sleep(interval or self._cfg.prewarm_interval)
