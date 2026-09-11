"""渠道凭据加密（docs/11 §7.2）：AES-256-GCM，密文格式 ``v1:<kid>:<base64url(nonce||ct||tag)>``。

主密钥只从环境变量读取（源码/示例/测试零凭据字面量）：
- ``HQ_MASTER_KEYS="k1=<hex64>[,k0=<hex64>...]"``：首个为当前加密密钥，其余仅用于解密（轮换并存）；
- 兼容 ``HQ_MASTER_KEY=<hex64>``，等价 ``k1=<hex64>``。
AAD 绑定 kid，防止密文在不同密钥间混淆移植。
"""
from __future__ import annotations

import base64
import ipaddress
import os
import secrets as _secrets
from urllib.parse import urlparse

from cryptography.hazmat.primitives.ciphers.aead import AESGCM

_PREFIX = "v1"
_NONCE_LEN = 12


class SecretError(RuntimeError):
    """主密钥未配置 / 密文格式或校验失败 / base_url 不合规。"""


def load_keys(env: dict[str, str] | None = None) -> dict[str, bytes]:
    """解析主密钥表：返回 {kid: 32B key}，插入顺序即优先级（首个为加密密钥）。"""
    src = env if env is not None else os.environ
    raw = (src.get("HQ_MASTER_KEYS") or "").strip()
    if not raw:
        legacy = (src.get("HQ_MASTER_KEY") or "").strip()
        raw = f"k1={legacy}" if legacy else ""
    if not raw:
        raise SecretError("HQ_MASTER_KEYS/HQ_MASTER_KEY not configured")
    keys: dict[str, bytes] = {}
    for part in raw.split(","):
        part = part.strip()
        if not part:
            continue
        kid, sep, hexkey = part.partition("=")
        if not sep:
            kid, hexkey = "k1", part
        kid = kid.strip()
        try:
            key = bytes.fromhex(hexkey.strip())
        except ValueError as exc:
            raise SecretError(f"master key {kid} is not hex") from exc
        if len(key) != 32:
            raise SecretError(f"master key {kid} must be 32-byte hex64")
        keys[kid] = key
    if not keys:
        raise SecretError("no master key parsed")
    return keys


def encrypt(plaintext: str, keys: dict[str, bytes] | None = None) -> str:
    ks = keys if keys is not None else load_keys()
    kid, key = next(iter(ks.items()))
    nonce = _secrets.token_bytes(_NONCE_LEN)
    ct = AESGCM(key).encrypt(nonce, plaintext.encode("utf-8"), kid.encode("utf-8"))
    b64 = base64.urlsafe_b64encode(nonce + ct).decode("ascii")
    return f"{_PREFIX}:{kid}:{b64}"


def decrypt(token: str, keys: dict[str, bytes] | None = None) -> str:
    ks = keys if keys is not None else load_keys()
    parts = token.split(":", 2)
    if len(parts) != 3 or parts[0] != _PREFIX:
        raise SecretError("bad token format")
    _, kid, b64 = parts
    key = ks.get(kid)
    if key is None:
        raise SecretError(f"unknown kid {kid}")
    try:
        blob = base64.urlsafe_b64decode(b64.encode("ascii"))
        if len(blob) <= _NONCE_LEN:
            raise ValueError("blob too short")
        pt = AESGCM(key).decrypt(blob[:_NONCE_LEN], blob[_NONCE_LEN:], kid.encode("utf-8"))
    except SecretError:
        raise
    except Exception as exc:  # noqa: BLE001 统一收口为 SecretError
        raise SecretError("decrypt failed") from exc
    return pt.decode("utf-8")


def mask_secret(secret: str) -> str:
    """日志/列表脱敏：sk-***last4（docs/11 §7.2 口径）。"""
    if len(secret) <= 8:
        return "***"
    return f"{secret[:3]}***{secret[-4:]}"


def validate_base_url(url: str, allow_private: bool = False) -> str:
    """结构化 SSRF 防护（写入与读取双侧调用）：仅 http/https；默认拒绝环回/私有/保留地址。

    allow_private=True 用于【管理员配置】的 LLM 渠道 base_url（内部令牌保护，内网 LLM 网关
    如 OneAPI/Dify/自建是合法场景，B16 端到端实测修正）；出站 webhook（开发者 hook_url）
    等不可信目标仍走默认严格模式。只做结构与 IP 字面量校验（写时不解析 DNS，避免离线误伤）。
    """
    u = urlparse((url or "").strip())
    if u.scheme not in ("http", "https") or not u.netloc:
        raise SecretError(f"base_url must be absolute http/https: {url!r}")
    host = (u.hostname or "").lower()
    if not host:
        raise SecretError(f"base_url host rejected: {url}")
    if allow_private:
        return u.geturl().rstrip("/")
    if host == "localhost" or host.endswith(".localhost") or host.endswith(".local"):
        raise SecretError(f"base_url host rejected: {url}")
    try:
        ip = ipaddress.ip_address(host)
    except ValueError:
        return u.geturl().rstrip("/")
    if not ip.is_global:
        raise SecretError(f"base_url private/reserved address rejected: {url}")
    return u.geturl().rstrip("/")
