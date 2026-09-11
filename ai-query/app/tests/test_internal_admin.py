"""internal admin 单测：guard fail-closed / 恒时比较路径 / pool 缺失降级（不依赖真实 MySQL）。"""
from __future__ import annotations

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))  # ai-query/ 进入 path

from fastapi import FastAPI, Request
from fastapi.testclient import TestClient

from app.api import internal


def _client(token: str | None) -> TestClient:
    app = FastAPI()
    app.include_router(internal.router)
    app.state.pool = None  # 不依赖 MySQL：通过 guard 的请求会得到 50310
    if token is not None:
        import os
        os.environ["INTERNAL_TOKEN"] = token
    return TestClient(app, raise_server_exceptions=False)


def test_guard_fail_closed_when_token_unset(monkeypatch):
    monkeypatch.delenv("INTERNAL_TOKEN", raising=False)
    c = _client(None)
    r = c.get("/internal/v1/llm/providers")
    assert r.status_code == 403 and r.json()["code"] == 40301  # 未配置 → 整组关闭


def test_guard_rejects_wrong_token(monkeypatch):
    monkeypatch.setenv("INTERNAL_TOKEN", "tok-correct")
    c = _client("tok-correct")
    r = c.get("/internal/v1/llm/providers", headers={"X-Internal-Token": "tok-wrong"})
    assert r.status_code == 401 and r.json()["code"] == 40103


def test_pool_disabled_returns_503(monkeypatch):
    monkeypatch.setenv("INTERNAL_TOKEN", "tok-correct")
    c = _client("tok-correct")
    r = c.get("/internal/v1/llm/providers", headers={"X-Internal-Token": "tok-correct"})
    assert r.status_code == 503 and r.json()["code"] == 50310
    r2 = c.post("/internal/v1/llm/providers", headers={"X-Internal-Token": "tok-correct"},
                json={"name": "ark", "base_url": "https://x.test/v1", "api_key": "k"})
    assert r2.status_code == 503 and r2.json()["code"] == 50310
