"""内部管理接口（X-Internal-Token，docs/11 §4 鉴权矩阵）：llm_provider/llm_model CRUD + fewshot 标注。

- INTERNAL_TOKEN 未配置 → 整组 403（fail-closed，docs/11 §4）；
- token 校验恒时比较（hmac.compare_digest）；
- api_key 只在请求体出现（内网传入），服务端加密入库；任何响应只返回脱敏形态；
- 写后 repo.invalidate()，30s 缓存立即失效。
"""
from __future__ import annotations

import hmac
import json
import os

from fastapi import APIRouter, Request
from fastapi.responses import JSONResponse
from pydantic import BaseModel, Field

from ..domain.condition import parse_condition, to_json
from ..infrastructure.secrets import SecretError, encrypt, mask_secret, validate_base_url

router = APIRouter(prefix="/internal/v1/llm")
fewshot_router = APIRouter(prefix="/internal/v1/fewshot")

_MODEL_TYPES = ("chat", "embedding", "image")


def _guard(request: Request) -> tuple[bool, int]:
    """返回 (ok, err_code)。未配置 token=功能未开启 40301；不匹配=40103。"""
    expected = os.environ.get("INTERNAL_TOKEN", "").strip()
    if not expected:
        return False, 40301
    got = request.headers.get("x-internal-token", "")
    return hmac.compare_digest(expected.encode("utf-8"), got.encode("utf-8")), 40103


def _pool(request: Request):
    return getattr(request.app.state, "pool", None)


def _repo(request: Request):
    return request.app.state.repo


def _err(code: int, msg: str, http: int):
    """信封错误响应：HTTP status 与 code 分段对齐（docs/11 §3）。"""
    return JSONResponse({"code": code, "msg": msg}, status_code=http)


@router.get("/providers")
async def list_providers(request: Request):
    ok, code = _guard(request)
    if not ok:
        return _err(code, "unauthorized", 401 if code == 40103 else 403)
    pool = _pool(request)
    if pool is None:
        return _err(50310, "mysql pool disabled", 503)
    async with pool.acquire() as conn:
        async with conn.cursor() as cur:
            await cur.execute(
                "SELECT id, name, base_url, api_key_enc, extra, enabled FROM llm_provider ORDER BY id")
            providers = [
                {
                    "id": r[0], "name": r[1], "base_url": r[2],
                    "api_key": mask_secret(r[3].split(":", 2)[-1] if r[3] else ""),
                    "extra": r[4], "enabled": bool(r[5]),
                } for r in await cur.fetchall()
            ]
            await cur.execute(
                "SELECT id, provider_id, model_id, model_name, model_type, weight, max_tokens, enabled "
                "FROM llm_model ORDER BY provider_id, id")
            models = [
                {"id": r[0], "provider_id": r[1], "model_id": r[2], "model_name": r[3],
                 "model_type": r[4], "weight": r[5], "max_tokens": r[6], "enabled": bool(r[7])}
                for r in await cur.fetchall()
            ]
    return {"code": 0, "msg": "ok", "data": {"providers": providers, "models": models}}


class ProviderReq(BaseModel):
    name: str = Field(min_length=1, max_length=64)
    base_url: str = Field(min_length=8, max_length=256)
    api_key: str = Field(min_length=1, max_length=256)
    extra: dict | None = None
    enabled: bool = True


@router.post("/providers")
async def create_provider(req: ProviderReq, request: Request):
    ok, code = _guard(request)
    if not ok:
        return _err(code, "unauthorized", 401 if code == 40103 else 403)
    pool = _pool(request)
    if pool is None:
        return _err(50310, "mysql pool disabled", 503)
    try:
        base_url = validate_base_url(req.base_url, allow_private=True)
        enc = encrypt(req.api_key)
    except SecretError as exc:
        return _err(40010, str(exc), 400)
    extra_json = json.dumps(req.extra, ensure_ascii=False) if req.extra else None
    async with pool.acquire() as conn:
        async with conn.cursor() as cur:
            await cur.execute("SELECT id FROM llm_provider WHERE name=%s", (req.name,))
            row = await cur.fetchone()
            if row:
                return _err(40902, f"provider exists: {req.name}", 409)
            await cur.execute("SELECT IFNULL(MAX(id),0)+1 FROM llm_provider")
            next_id = (await cur.fetchone())[0]
            await cur.execute(
                "INSERT INTO llm_provider (id, name, base_url, api_key_enc, extra, enabled) "
                "VALUES (%s,%s,%s,%s,%s,%s)",
                (next_id, req.name, base_url, enc, extra_json, 1 if req.enabled else 0))
    _repo(request).invalidate()
    return {"code": 0, "msg": "ok", "data": {"id": next_id, "name": req.name}}


class ProviderPatch(BaseModel):
    base_url: str | None = None
    api_key: str | None = None
    extra: dict | None = None
    enabled: bool | None = None


@router.put("/providers/{provider_id}")
async def update_provider(provider_id: int, req: ProviderPatch, request: Request):
    ok, code = _guard(request)
    if not ok:
        return _err(code, "unauthorized", 401 if code == 40103 else 403)
    pool = _pool(request)
    if pool is None:
        return _err(50310, "mysql pool disabled", 503)
    sets: list[str] = []
    args: list = []
    try:
        if req.base_url is not None:
            sets.append("base_url=%s")
            args.append(validate_base_url(req.base_url, allow_private=True))
        if req.api_key is not None:
            sets.append("api_key_enc=%s")
            args.append(encrypt(req.api_key))
        if req.extra is not None:
            sets.append("extra=%s")
            args.append(json.dumps(req.extra, ensure_ascii=False))
    except SecretError as exc:
        return _err(40010, str(exc), 400)
    if req.enabled is not None:
        sets.append("enabled=%s")
        args.append(1 if req.enabled else 0)
    if not sets:
        return _err(40010, "nothing to update", 400)
    args.append(provider_id)
    async with pool.acquire() as conn:
        async with conn.cursor() as cur:
            n = await cur.execute(f"UPDATE llm_provider SET {', '.join(sets)} WHERE id=%s", args)
    if n == 0:
        return _err(40401, "provider not found", 404)
    _repo(request).invalidate()
    return {"code": 0, "msg": "ok"}


class ModelReq(BaseModel):
    provider_id: int
    model_id: str = Field(min_length=1, max_length=128)
    model_name: str = Field(min_length=1, max_length=128)
    model_type: str = Field(default="chat")
    weight: int = Field(default=1, ge=1, le=100)
    max_tokens: int = Field(default=2048, ge=64, le=32768)
    enabled: bool = True


@router.post("/models")
async def create_model(req: ModelReq, request: Request):
    ok, code = _guard(request)
    if not ok:
        return _err(code, "unauthorized", 401 if code == 40103 else 403)
    pool = _pool(request)
    if pool is None:
        return _err(50310, "mysql pool disabled", 503)
    if req.model_type not in _MODEL_TYPES:
        return _err(40010, f"model_type must be one of {_MODEL_TYPES}", 400)
    async with pool.acquire() as conn:
        async with conn.cursor() as cur:
            await cur.execute("SELECT 1 FROM llm_provider WHERE id=%s", (req.provider_id,))
            if not await cur.fetchone():
                return _err(40401, "provider not found", 404)
            await cur.execute("SELECT IFNULL(MAX(id),0)+1 FROM llm_model")
            next_id = (await cur.fetchone())[0]
            await cur.execute(
                "INSERT INTO llm_model (id, provider_id, model_id, model_name, model_type, "
                "weight, max_tokens, enabled) VALUES (%s,%s,%s,%s,%s,%s,%s,%s)",
                (next_id, req.provider_id, req.model_id, req.model_name, req.model_type,
                 req.weight, req.max_tokens, 1 if req.enabled else 0))
    _repo(request).invalidate()
    return {"code": 0, "msg": "ok", "data": {"id": next_id}}


class ModelPatch(BaseModel):
    model_name: str | None = None
    weight: int | None = Field(default=None, ge=1, le=100)
    enabled: bool | None = None


@router.put("/models/{model_id}")
async def update_model(model_id: int, req: ModelPatch, request: Request):
    ok, code = _guard(request)
    if not ok:
        return _err(code, "unauthorized", 401 if code == 40103 else 403)
    pool = _pool(request)
    if pool is None:
        return _err(50310, "mysql pool disabled", 503)
    sets: list[str] = []
    args: list = []
    if req.model_name is not None:
        sets.append("model_name=%s")
        args.append(req.model_name)
    if req.weight is not None:
        sets.append("weight=%s")
        args.append(req.weight)
    if req.enabled is not None:
        sets.append("enabled=%s")
        args.append(1 if req.enabled else 0)
    if not sets:
        return _err(40010, "nothing to update", 400)
    args.append(model_id)
    async with pool.acquire() as conn:
        async with conn.cursor() as cur:
            n = await cur.execute(f"UPDATE llm_model SET {', '.join(sets)} WHERE id=%s", args)
    if n == 0:
        return _err(40401, "model not found", 404)
    _repo(request).invalidate()
    return {"code": 0, "msg": "ok"}


# ---- B27：fewshot 样本管理（数据飞轮人工标注） ----

def _fs(request: Request):
    return getattr(request.app.state, "fewshots", None)


def _guarded_pool(request: Request):
    ok, code = _guard(request)
    if not ok:
        return None, _err(code, "unauthorized", 401 if code == 40103 else 403)
    if _pool(request) is None:
        return None, _err(50310, "mysql pool disabled", 503)
    return _fs(request), None


@fewshot_router.get("")
async def list_fewshot(request: Request, status: str | None = None, limit: int = 100):
    fs, err = _guarded_pool(request)
    if err is not None:
        return err
    rows = await fs.list_page(status, limit=min(max(limit, 1), 500))
    return {"code": 0, "msg": "ok", "data": {"samples": rows}}


@fewshot_router.get("/stats")
async def fewshot_stats(request: Request):
    fs, err = _guarded_pool(request)
    if err is not None:
        return err
    return {"code": 0, "msg": "ok", "data": {"counts": await fs.stats()}}


class FewshotPatch(BaseModel):
    status: str | None = Field(default=None, pattern="^(ACTIVE|PENDING|REJECTED)$")
    condition_json: str | None = None  # 标注补条件：过白名单校验后存规范形


@fewshot_router.patch("/{sample_id}")
async def patch_fewshot(sample_id: int, req: FewshotPatch, request: Request):
    fs, err = _guarded_pool(request)
    if err is not None:
        return err
    if req.status is None and req.condition_json is None:
        return _err(40010, "nothing to update", 400)
    if req.condition_json is not None:
        try:
            canonical = to_json(parse_condition(req.condition_json))
        except ValueError as exc:
            return _err(40010, f"condition_json rejected by whitelist: {exc}", 400)
        await fs.set_condition(sample_id, canonical)
    if req.status is not None:
        await fs.set_status(sample_id, req.status)
    return {"code": 0, "msg": "ok"}
