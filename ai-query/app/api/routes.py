"""FastAPI 路由（api 层，docs/04 §6 信封 {code,msg,data}；错误响应带正确 HTTP status，docs/11 §3）。"""
from __future__ import annotations

import asyncio
import json

from fastapi import APIRouter, Request, Response
from fastapi.responses import JSONResponse
from prometheus_client import CONTENT_TYPE_LATEST, generate_latest
from pydantic import BaseModel, Field

from ..application.nl2cond import AIQueryService, UnknownCondition
from ..domain.condition import MAX_PRICE, MAX_PCT, MAX_VOLUME, parse_condition, to_json
from ..infrastructure.llm import LLMError

router = APIRouter()


def build_meta() -> dict:
    """白名单元数据（B26 meta 驱动前端：表单渲染/订阅映射的单一事实源，ADR-042 §8）。

    rule_type = 订阅转换目标（VOLUME_ABOVE → INDICATOR/VOLUME）；组合条件不提供订阅映射
    （规则侧扁平，前端 conditionToRuleInput 拒绝）。数值边界与 domain 常量同源。
    """
    return {
        "condition_version": 2,
        "groups": {"max_depth": 2, "max_items": 10},
        "query_types": [
            {
                "type": "PRICE_ABOVE", "label": "价格高于", "rule_type": "PRICE_ABOVE",
                "fields": [{"name": "threshold", "label": "价格(元)", "type": "float", "gt": 0, "le": MAX_PRICE}],
            },
            {
                "type": "PRICE_BELOW", "label": "价格低于", "rule_type": "PRICE_BELOW",
                "fields": [{"name": "threshold", "label": "价格(元)", "type": "float", "gt": 0, "le": MAX_PRICE}],
            },
            {
                "type": "PRICE_RANGE", "label": "价格区间", "rule_type": "PRICE_RANGE",
                "fields": [
                    {"name": "low", "label": "最低价(元)", "type": "float", "gt": 0, "le": MAX_PRICE},
                    {"name": "high", "label": "最高价(元)", "type": "float", "gt": 0, "le": MAX_PRICE},
                ],
            },
            {
                "type": "PCT_CHANGE", "label": "涨跌幅(%)", "rule_type": "PCT_CHANGE",
                "fields": [
                    {"name": "threshold", "label": "涨跌幅(%)", "type": "float", "ne": 0, "ale": MAX_PCT},
                    {"name": "direction", "label": "方向", "type": "enum",
                     "values": ["up", "down", "both"], "required": False},
                    {"name": "limit", "label": "涨跌停预警", "type": "bool", "required": False},
                ],
            },
            {
                "type": "VOLUME_ABOVE", "label": "成交量高于(股)", "rule_type": "INDICATOR",
                "fields": [{"name": "threshold", "label": "成交量(股)", "type": "float", "gt": 0, "le": MAX_VOLUME}],
            },
        ],
    }


class QueryReq(BaseModel):
    q: str = Field(min_length=1, max_length=500)
    limit: int = Field(default=50, ge=1, le=200)


def get_service(request: Request) -> AIQueryService:
    return request.app.state.svc  # type: ignore[no-any-return]


def _client_ip(request: Request) -> str | None:
    xff = request.headers.get("x-forwarded-for")
    if xff:
        return xff.split(",")[0].strip() or None
    return request.client.host if request.client else None


@router.get("/healthz")
async def healthz() -> dict:
    return {"code": 0, "msg": "ok"}


@router.get("/metrics")
async def metrics() -> Response:
    return Response(generate_latest(), media_type=CONTENT_TYPE_LATEST)


@router.get("/api/v1/ai/meta")
async def ai_meta() -> dict:
    """白名单元数据（静态只读，无 LLM 调用；前端表单渲染/订阅映射契约，docs/04 §6）。"""
    return {"code": 0, "msg": "ok", "data": build_meta()}


@router.post("/api/v1/ai/query")
async def ai_query(req: QueryReq, request: Request) -> Response:
    try:
        data = await get_service(request).query(req.q, req.limit, client_ip=_client_ip(request))
    except UnknownCondition as exc:
        return JSONResponse({"code": 42201, "msg": f"unsupported condition: {exc}"}, status_code=422)
    except ValueError as exc:
        return JSONResponse({"code": 40001, "msg": str(exc)}, status_code=400)
    except LLMError as exc:
        if exc.status == 429:  # 日配额/上游限流正名（原误入 503 兜底）；429 必带 Retry-After（docs/11 §3）
            return JSONResponse(
                {"code": 42901, "msg": str(exc)}, status_code=429,
                headers={"Retry-After": str(exc.retry_after or 60)},
            )
        return JSONResponse({"code": 50310, "msg": "ai query unavailable"}, status_code=503)
    except Exception:  # noqa: BLE001 LLM/CK 上游故障降级为 5xxxx
        return JSONResponse({"code": 50310, "msg": "ai query unavailable"}, status_code=503)
    return {"code": 0, "msg": "ok", "data": data}


class ScreenReq(BaseModel):
    condition: dict
    limit: int = Field(default=50, ge=1, le=200)


@router.post("/api/v1/ai/screen")
async def ai_screen(req: ScreenReq, request: Request) -> Response:
    """条件直查（B29 条件编排器执行口）：积木编译出的规范化条件跳过 LLM 直接筛 CK，
    与 /ai/query 共用白名单校验/组合树/CK 片段构建（数值参数绑定，支持 all/any 组合树）。"""
    try:
        cond = parse_condition(req.condition)
    except ValueError as exc:
        return JSONResponse({"code": 42201, "msg": f"unsupported condition: {exc}"}, status_code=422)
    try:
        rows = await asyncio.to_thread(get_service(request).ck.screen, cond, limit=req.limit)
    except Exception:  # noqa: BLE001 CK 上游故障降级为 5xxxx
        return JSONResponse({"code": 50310, "msg": "ai query unavailable"}, status_code=503)
    return {
        "code": 0, "msg": "ok",
        "data": {"condition": json.loads(to_json(cond)), "symbols": rows, "condition_version": 2},
    }
