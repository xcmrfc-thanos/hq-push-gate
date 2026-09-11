"""ai-query 进程入口：NL2Condition 自然语言筛选（docs/04 §6 / docs/09 §5.1 Branch 5；B16-A2 lifespan 化）。

启动：py -3 -m uvicorn app.main:app --host 127.0.0.1 --port 23041（工作目录 ai-query/）

装配链（全部可降级，见各模块回退链）：
- MySQL（HQ_MYSQL_* 配齐才建池）：llm_provider/llm_model 渠道源；未配或不可达 → LLM_* env 单渠道
- Redis（HQ_REDIS_URL）：熔断态/日配额/条件缓存；不可用 → 进程内兜底
- LLM 预热：启动 + 每 60s GET {base_url}/models 建 TLS 连接入池
"""
from __future__ import annotations

import asyncio
import logging
import os
from contextlib import asynccontextmanager

import aiomysql
import httpx
import redis.asyncio as aioredis
from fastapi import FastAPI

from .api.internal import router as internal_router
from .api.internal import fewshot_router
from .api.routes import router
from .application.nl2cond import AIQueryService
from .infrastructure.clickhouse import ClickHouseReader
from .infrastructure.fewshot_repo import FewshotRepo
from .infrastructure.llm_gateway import ChannelState, GatewayConfig, LLMGateway, Quota
from .infrastructure.llm_repo import ChannelRepo
from .infrastructure.redis_cache import CondCache

log = logging.getLogger("ai-query")

app = FastAPI(title="hq-push-gate ai-query", version="0.2.0")
app.include_router(router)
app.include_router(internal_router)
app.include_router(fewshot_router)


def _env(name: str, default: str = "") -> str:
    return os.environ.get(name, default).strip()


def _mysql_pool_kwargs() -> dict | None:
    """HQ_MYSQL_HOST 配齐才启用 DB 渠道源；密码无默认值（docs/11 §7.2 零字面量）。"""
    host = _env("HQ_MYSQL_HOST")
    if not host:
        return None
    password = os.environ.get("HQ_MYSQL_PASSWORD", "")
    if not password:
        log.warning("HQ_MYSQL_HOST set but HQ_MYSQL_PASSWORD empty; DB channel source disabled")
        return None
    return {
        "host": host,
        "port": int(_env("HQ_MYSQL_PORT", "23001")),
        "user": _env("HQ_MYSQL_USER", "hqpush"),
        "password": password,
        "db": _env("HQ_MYSQL_DB", "hqpush"),
    }


@asynccontextmanager
async def lifespan(app: FastAPI):
    # prod 凭据门禁（docs/12 §1.1 #18）：弱/缺凭据 fail-fast 拒绝启动
    if _env("HQ_PROFILE") == "prod":
        missing = []
        if not (_env("HQ_MASTER_KEYS") or _env("HQ_MASTER_KEY")) and not _env("LLM_API_KEY"):
            missing.append("HQ_MASTER_KEYS（DB 渠道解密）或 LLM_API_KEY（env 回退渠道）")
        if not _env("INTERNAL_TOKEN"):
            missing.append("INTERNAL_TOKEN（渠道 admin 鉴权）")
        if missing:
            raise RuntimeError(f"prod profile missing credentials: {'; '.join(missing)}")

    pool = None
    kwargs = _mysql_pool_kwargs()
    if kwargs:
        try:
            pool = await aiomysql.create_pool(minsize=1, maxsize=5, autocommit=True, **kwargs)
        except Exception as exc:  # noqa: BLE001 DB 不可达 → env 回退，服务照常起
            log.warning("mysql pool disabled: %s", exc)

    redis_url = _env("HQ_REDIS_URL", "redis://127.0.0.1:23002/0")
    try:
        rdb = aioredis.from_url(redis_url, socket_timeout=2, decode_responses=True)
        await rdb.ping()
    except Exception as exc:  # noqa: BLE001
        log.warning("redis unavailable, in-process fallback state: %s", exc)
        rdb = None

    http = httpx.AsyncClient(
        limits=httpx.Limits(max_keepalive_connections=20, max_connections=100, keepalive_expiry=300.0),
        timeout=httpx.Timeout(30.0, connect=3.0),
    )
    repo = ChannelRepo(pool)
    gw = LLMGateway(
        repo=repo,
        http=http,
        state=ChannelState(rdb),
        quota=Quota(
            rdb,
            daily_global=int(_env("LLM_DAILY_GLOBAL", "2000")),
            daily_per_ip=int(_env("LLM_DAILY_PER_IP", "50")),
        ),
        cfg=GatewayConfig(max_concurrency=int(_env("LLM_MAX_CONCURRENCY", "10"))),
    )
    prewarm_task = asyncio.create_task(gw.prewarm_loop())
    fewshots = FewshotRepo(pool)
    app.state.svc = AIQueryService(llm=gw, ck=ClickHouseReader(), cache=CondCache(), fewshots=fewshots)
    app.state.gw = gw
    app.state.pool = pool
    app.state.repo = repo
    app.state.fewshots = fewshots
    try:
        yield
    finally:
        prewarm_task.cancel()
        await http.aclose()
        if pool is not None:
            pool.close()
            await pool.wait_closed()
        if rdb is not None:
            await rdb.aclose()


app.router.lifespan_context = lifespan
