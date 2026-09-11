"""FastAPI 路由（api 层，docs/04 §6 信封 {code,msg,data}；错误响应带正确 HTTP status，docs/11 §3）。"""
from __future__ import annotations

from fastapi import APIRouter, Request, Response
from fastapi.responses import JSONResponse
from prometheus_client import CONTENT_TYPE_LATEST, generate_latest
from pydantic import BaseModel, Field

from ..application.nl2cond import AIQueryService, UnknownCondition
from ..infrastructure.llm import LLMError

router = APIRouter()


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
