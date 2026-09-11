"""LLM 网关单测：加权轮询 / 渠道回退 / 冷却熔断 / 日配额 / ark 差异化（httpx.MockTransport，内存态，无外部依赖）。"""
from __future__ import annotations

import asyncio
import json

import httpx
import pytest

from app.infrastructure.llm import Channel, LLMError
from app.infrastructure.llm_gateway import ChannelState, GatewayConfig, LLMGateway, Quota


def _ch(db_id: int, weight: int = 1, host: str = "up.test") -> Channel:
    return Channel(model_db_id=db_id, name=f"ch{db_id}", base_url=f"https://{host}",
                   api_key="k-test", model_id="m-test", weight=weight)


def _ok(content: str = '{"type":"UNKNOWN"}') -> httpx.Response:
    return httpx.Response(200, json={"choices": [{"message": {"content": content}}]})


class FakeRepo:
    def __init__(self, channels: list[Channel]) -> None:
        self.channels = channels

    async def list_channels(self, model_type: str = "chat") -> list[Channel]:
        return list(self.channels)


def _gw(channels: list[Channel], handler) -> LLMGateway:
    http = httpx.AsyncClient(transport=httpx.MockTransport(handler))
    return LLMGateway(FakeRepo(channels), http, ChannelState(None), Quota(None, 0, 0), GatewayConfig())


def _run(coro):
    return asyncio.run(coro)


def test_fallback_on_500_then_success():
    async def case():
        async def handler(request: httpx.Request) -> httpx.Response:
            return httpx.Response(500, text="boom") if request.url.host == "a.test" else _ok()
        gw = _gw([_ch(1, host="a.test"), _ch(2, host="b.test")], handler)
        text, name = await gw.complete("sys", "user")
        assert text.startswith("{") and name == "ch2"
        assert await gw._state.fail_count_incr(1) == 2  # 首次失败已计 1（本断言再 +1）
    _run(case())


def test_auth_error_cools_down_immediately():
    async def case():
        async def handler(request: httpx.Request) -> httpx.Response:
            return httpx.Response(401, text="bad key") if request.url.host == "a.test" else _ok()
        gw = _gw([_ch(1, host="a.test"), _ch(2, host="b.test")], handler)
        text, name = await gw.complete("sys", "user")
        assert name == "ch2"
        assert await gw._state.in_cooldown(1) is True
        # 后续请求直接跳过 ch1
        _, name2 = await gw.complete("sys", "user")
        assert name2 == "ch2"
    _run(case())


def test_all_channels_fail_raises():
    async def case():
        async def handler(request: httpx.Request) -> httpx.Response:
            return httpx.Response(500, text="down")
        gw = _gw([_ch(1, host="a.test"), _ch(2, host="b.test")], handler)
        with pytest.raises(LLMError):
            await gw.complete("sys", "user")
    _run(case())


def test_smooth_weighted_round_robin():
    async def case():
        seen: list[str] = []

        async def handler(request: httpx.Request) -> httpx.Response:
            seen.append("A" if request.url.host == "a.test" else "B")
            return _ok()
        gw = _gw([_ch(1, weight=2, host="a.test"), _ch(2, weight=1, host="b.test")], handler)
        for _ in range(6):
            await gw.complete("sys", "user")
        assert seen == ["A", "B", "A", "A", "B", "A"]  # 权重 2:1 平滑序列
    _run(case())


def test_ark_body_and_user_agent():
    async def case():
        captured: dict = {}

        async def handler(request: httpx.Request) -> httpx.Response:
            captured["json"] = json.loads(request.content.decode())
            captured["ua"] = request.headers.get("user-agent")
            return _ok()
        ch = _ch(1)
        ch.thinking = False
        ch.user_agent = "ua-test/1.0"
        gw = _gw([ch], handler)
        await gw.complete("sys", "user", max_tokens=99)
        assert captured["json"]["thinking"] == {"type": "disabled"}
        assert captured["json"]["max_tokens"] == 99
        assert captured["ua"] == "ua-test/1.0"
        assert captured["json"]["temperature"] == 0
    _run(case())


def test_quota_blocks_after_limit():
    async def case():
        async def handler(request: httpx.Request) -> httpx.Response:
            return _ok()
        http = httpx.AsyncClient(transport=httpx.MockTransport(handler))
        gw = LLMGateway(FakeRepo([_ch(1)]), http, ChannelState(None), Quota(None, 1, 0), GatewayConfig())
        await gw.complete("sys", "user", client_ip="1.2.3.4")
        with pytest.raises(LLMError) as ei:
            await gw.complete("sys", "user", client_ip="1.2.3.4")
        assert ei.value.status == 429
    _run(case())


def test_per_ip_quota():
    async def case():
        async def handler(request: httpx.Request) -> httpx.Response:
            return _ok()
        http = httpx.AsyncClient(transport=httpx.MockTransport(handler))
        gw = LLMGateway(FakeRepo([_ch(1)]), http, ChannelState(None), Quota(None, 0, 1), GatewayConfig())
        await gw.complete("sys", "user", client_ip="1.1.1.1")
        with pytest.raises(LLMError):
            await gw.complete("sys", "user", client_ip="1.1.1.1")
        # 其他 IP 不受影响
        await gw.complete("sys", "user", client_ip="2.2.2.2")
    _run(case())


def test_cooldown_expiry_half_open():
    async def case():
        state = ChannelState(None)
        await state.cooldown(7, 0.01)
        assert await state.in_cooldown(7) is True
        await asyncio.sleep(0.03)
        assert await state.in_cooldown(7) is False  # TTL 过期即半开
    _run(case())


def test_no_channel_raises():
    async def case():
        gw = _gw([], lambda request: _ok())
        with pytest.raises(LLMError):
            await gw.complete("sys", "user")
    _run(case())
