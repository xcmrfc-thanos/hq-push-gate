"""secrets 单测：AES-256-GCM roundtrip / 轮换 / 篡改 / kid 绑定 / base_url 校验。密钥全部随机生成。"""
import os

import pytest

from app.infrastructure.secrets import (
    SecretError, decrypt, encrypt, load_keys, mask_secret, validate_base_url,
)


def _keys(kid: str = "k1") -> dict[str, bytes]:
    return {kid: os.urandom(32)}


def test_roundtrip():
    ks = _keys()
    token = encrypt("sk-abc123", ks)
    assert token.startswith("v1:k1:")
    assert decrypt(token, ks) == "sk-abc123"


def test_rotation_two_keys():
    k1, k0 = _keys("k1"), _keys("k0")
    both = {**k1, **k0}  # 首个为当前加密密钥
    assert encrypt("s", both).split(":")[1] == "k1"
    old = encrypt("s2", k0)
    assert decrypt(old, both) == "s2"  # 旧密文仍可解（轮换并存）


def test_tamper_rejected():
    ks = _keys()
    token = encrypt("secret", ks)
    bad = token[:-4] + ("AAAA" if not token.endswith("AAAA") else "BBBB")
    with pytest.raises(SecretError):
        decrypt(bad, ks)


def test_wrong_key_rejected():
    token = encrypt("s", _keys("k1"))
    with pytest.raises(SecretError):
        decrypt(token, _keys("k1"))


def test_kid_binding():
    ks = _keys()
    token = encrypt("s", ks)
    forged = token.replace(":k1:", ":k2:", 1)
    with pytest.raises(SecretError):
        decrypt(forged, {**ks, "k2": os.urandom(32)})


def test_load_keys_env_compat():
    hexkey = os.urandom(32).hex()
    assert load_keys({"HQ_MASTER_KEY": hexkey}) == {"k1": bytes.fromhex(hexkey)}
    assert list(load_keys({"HQ_MASTER_KEYS": f"k2={hexkey}"})) == ["k2"]
    with pytest.raises(SecretError):
        load_keys({})
    with pytest.raises(SecretError):
        load_keys({"HQ_MASTER_KEY": "ab_cd"})  # 非 hex


def test_mask_secret():
    assert mask_secret("sk-1234567890abcdef") == "sk-***cdef"
    assert mask_secret("short") == "***"


def test_validate_base_url():
    ok = "https://ark.cn-beijing.volces.com/api/coding/v3/"
    assert validate_base_url(ok) == "https://ark.cn-beijing.volces.com/api/coding/v3"
    assert validate_base_url("https://8.8.8.8/v1") == "https://8.8.8.8/v1"
    for bad in [
        "ftp://x.com", "not-a-url", "",
        "http://localhost:8080/v1", "https://box.local/v1",
        "https://127.0.0.1/v1", "https://10.1.2.3", "https://192.168.1.1", "https://169.254.1.1",
    ]:
        with pytest.raises(SecretError):
            validate_base_url(bad)


def test_validate_base_url_allow_private_admin_channel():
    # 管理员配置的 LLM 渠道允许内网网关（OneAPI/自建/mock），B16 端到端实测修正
    assert validate_base_url("http://127.0.0.1:59990/v1", allow_private=True) == "http://127.0.0.1:59990/v1"
    assert validate_base_url("http://10.0.0.5:3000", allow_private=True) == "http://10.0.0.5:3000"
    # 但协议/格式非法仍拒
    with pytest.raises(SecretError):
        validate_base_url("ftp://127.0.0.1/v1", allow_private=True)
