"""LLM 传输层：OpenAI 兼容 chat/completions 单发（docs/12 B16-A2）。

上层 LLMGateway（llm_gateway.py）负责多渠道加权轮询/冷却熔断/日配额/预热；
本模块只负责"对单个渠道发一次请求"，以及 MySQL 无渠道/不可达时的 env 单渠道回退。
保留仓库根 .env 的 LLM_* 键加载（os.environ 优先，K8s Secret 注入不经 .env）。
"""
from __future__ import annotations

import os
from dataclasses import dataclass
from pathlib import Path

import httpx

_REPO_ROOT = Path(__file__).resolve().parents[3]

DEFAULT_USER_AGENT = "claude-cli/2.0.14 (external, cli)"


def load_env() -> dict[str, str]:
    """解析仓库根 .env（仅 LLM_* 键；# 注释行忽略）。

    环境变量始终优先（K8s envFrom/Secret 注入的容器环境没有 .env 文件）。
    """
    out: dict[str, str] = {}
    path = _REPO_ROOT / ".env"
    if path.exists():
        for line in path.read_text(encoding="utf-8").splitlines():
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            k, _, v = line.partition("=")
            k, v = k.strip(), v.strip().strip('"')
            if k.startswith("LLM_"):
                out.setdefault(k, v)
    for k, v in os.environ.items():
        if k.startswith("LLM_") and v.strip():
            out[k] = v
    return out


class LLMError(RuntimeError):
    """LLM 调用失败（网络/上游/空响应）。status>0 表示拿到了上游 HTTP 状态码。
    retry_after>0 时 HTTP 429 响应下发 Retry-After 头（docs/11 §3）。"""

    def __init__(self, msg: str, status: int = 0, retry_after: int = 0) -> None:
        super().__init__(msg)
        self.status = status
        self.retry_after = retry_after


@dataclass
class Channel:
    """轮询渠道 = provider×model（llm_model 行 + 关联 llm_provider 解密凭据）。"""

    model_db_id: int            # llm_model.id，熔断/配额键（env 回退渠道恒为 0）
    name: str                   # 展示名 provider/model_id 或 env:provider
    base_url: str
    api_key: str
    model_id: str
    weight: int = 1
    user_agent: str = DEFAULT_USER_AGENT
    thinking: bool | None = None    # 火山方舟思考开关；None=不传该字段
    max_tokens_cap: int = 2048


def channel_from_env(env: dict[str, str] | None = None) -> Channel | None:
    """env 单渠道回退（兼容现网 .env/K8s Secret 部署；未配置返回 None）。"""
    cfg = env or load_env()
    base_url = cfg.get("LLM_BASE_URL", "").strip().rstrip("/")
    api_key = cfg.get("LLM_API_KEY", "").strip()
    model = cfg.get("LLM_MODEL", "").strip()
    if not (base_url and api_key and model):
        return None
    provider = cfg.get("LLM_PROVIDER", "")
    thinking: bool | None = None
    if provider == "ark":
        thinking = cfg.get("LLM_THINKING", "true").lower() != "false"
    return Channel(
        model_db_id=0,
        name=f"env:{provider or 'default'}",
        base_url=base_url,
        api_key=api_key,
        model_id=model,
        user_agent=cfg.get("LLM_USER_AGENT", DEFAULT_USER_AGENT),
        thinking=thinking,
    )


async def complete_once(
    http: httpx.AsyncClient,
    ch: Channel,
    system: str,
    user: str,
    max_tokens: int = 300,
) -> str:
    """对单渠道发一次 chat/completions，返回 content；失败抛 LLMError（带上游状态码）。"""
    body: dict = {
        "model": ch.model_id,
        "messages": [
            {"role": "system", "content": system},
            {"role": "user", "content": user},
        ],
        "max_tokens": max(16, min(max_tokens, ch.max_tokens_cap)),
        "temperature": 0,
    }
    if ch.thinking is not None:  # 火山方舟思考开关；Coding Plan 端点按编码工具 UA 放行
        body["thinking"] = {"type": "enabled" if ch.thinking else "disabled"}
    resp = await http.post(
        f"{ch.base_url}/chat/completions",
        headers={"Authorization": f"Bearer {ch.api_key}", "User-Agent": ch.user_agent},
        json=body,
    )
    if resp.status_code != 200:
        raise LLMError(f"llm upstream {resp.status_code}: {resp.text[:200]}", status=resp.status_code)
    data = resp.json()
    try:
        content = data["choices"][0]["message"]["content"]
    except (KeyError, IndexError, TypeError) as exc:
        raise LLMError(f"bad llm response shape: {data}") from exc
    if not content:
        raise LLMError("empty llm content")
    return content
