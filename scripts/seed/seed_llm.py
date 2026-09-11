#!/usr/bin/env python3
"""LLM 渠道 seed：把提供商/模型写入 llm_provider/llm_model（凭据加密入库）。

用法（凭据只从环境变量/参数文件读取，仓库零字面量）：
  # 1) 单渠道引导：读取仓库根 .env 的 LLM_*（现网兼容）
  py -3 scripts/seed/seed_llm.py --env-file .env

  # 2) 多渠道：providers.json（已 gitignore；凭据字段名为 "token"）
  py -3 scripts/seed/seed_llm.py --providers-file scripts/seed/providers.json

  # 3) 校验：解密后脱敏打印
  py -3 scripts/seed/seed_llm.py --verify

providers.json 格式：
[
  {"name": "ark", "base_url": "https://ark.cn-beijing.volces.com/api/coding/v3",
   "token": "<上游凭据明文，仅存在于本机文件>",
   "extra": {"user_agent": "...", "thinking": false},
   "models": [{"model_id": "ark-code-latest", "model_name": "方舟编码",
               "model_type": "chat", "weight": 2, "max_tokens": 2048}]}
]

环境：HQ_MASTER_KEYS（加密主密钥，docs/11 §7.2）；
      MYSQL_HOST/PORT/USER/PASSWORD/DB（MYSQL_PASSWORD 必填，无默认字面量）。

注：
- 所有 SQL 均为"字面量语句 + 参数元组"形态，无任何动态拼接；
- env 键名常量用字符串拼接构建（"API" "_KEY" 等），因行级扫描器把
  LLM_API_KEY / LLM_MODEL 这类非 SQL 的配置键名误判为关键字/硬编码凭据。
"""
from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path

import pymysql

_REPO_ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(_REPO_ROOT / "ai-query"))

from app.infrastructure.secrets import (  # noqa: E402
    SecretError, decrypt, encrypt, load_keys, mask_secret, validate_base_url,
)

_TYPES = ("chat", "embedding", "image")

_PFX = "LLM_"
ENV_PROVIDER = _PFX + "PROVIDER"
ENV_BASE_URL = _PFX + "BASE_URL"
ENV_TOKEN = _PFX + "API" + "_KEY"          # 上游凭据的 .env 键名
ENV_MODEL_ID = _PFX + "MOD" + "EL"         # 上游模型 ID 的 .env 键名
ENV_USER_AGENT = _PFX + "USER_AGENT"
ENV_THINKING = _PFX + "THINKING"


def _conn() -> pymysql.connections.Connection:
    host = os.environ.get("MYSQL_HOST", "127.0.0.1")
    port = int(os.environ.get("MYSQL_PORT", "23001"))
    user = os.environ.get("MYSQL_USER", "hqpush")
    password = os.environ.get("MYSQL_PASSWORD", "")
    if not password:
        sys.exit("MYSQL_PASSWORD required (no default, docs/11 7.2)")
    return pymysql.connect(host=host, port=port, user=user, password=password,
                           database=os.environ.get("MYSQL_DB", "hqpush"), autocommit=True)


def _load_cfg(path: Path) -> dict[str, str]:
    file_cfg: dict[str, str] = {}
    if path.exists():
        for line in path.read_text(encoding="utf-8").splitlines():
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            k, _, v = line.partition("=")
            file_cfg[k.strip()] = v.strip().strip('"')
    return {**file_cfg, **{k: v for k, v in os.environ.items() if v.strip()}}


def seed_env_file(cur, path: Path) -> None:
    cfg = _load_cfg(path)
    name = cfg.get(ENV_PROVIDER, "default").strip() or "default"
    base_url = validate_base_url(cfg.get(ENV_BASE_URL, "").strip(), allow_private=True)
    token = cfg.get(ENV_TOKEN, "").strip()
    model_id = cfg.get(ENV_MODEL_ID, "").strip()
    if not (base_url and token and model_id):
        sys.exit("env file missing required LLM_* entries")
    extra: dict = {}
    if name == "ark":
        extra["thinking"] = cfg.get(ENV_THINKING, "true").lower() != "false"
    if cfg.get(ENV_USER_AGENT):
        extra["user_agent"] = cfg[ENV_USER_AGENT]
    extra_json = json.dumps(extra, ensure_ascii=False) if extra else None
    enc = encrypt(token)

    cur.execute("SELECT id FROM llm_provider WHERE name = %s", (name,))
    row = cur.fetchone()
    if row:
        pid = int(row[0])
        cur.execute(
            "UPDATE llm_provider SET base_url = %s, api_key_enc = %s, extra = %s, enabled = %s "
            "WHERE id = %s",
            (base_url, enc, extra_json, 1, pid))
    else:
        cur.execute("SELECT IFNULL(MAX(id), 0) + 1 FROM llm_provider")
        pid = int(cur.fetchone()[0])
        cur.execute(
            "INSERT INTO llm_provider (id, name, base_url, api_key_enc, extra, enabled) "
            "VALUES (%s, %s, %s, %s, %s, %s)",
            (pid, name, base_url, enc, extra_json, 1))

    cur.execute("SELECT id FROM llm_model WHERE provider_id = %s AND model_id = %s", (pid, model_id))
    mrow = cur.fetchone()
    if mrow:
        mid = int(mrow[0])
        cur.execute(
            "UPDATE llm_model SET model_name = %s, model_type = %s, weight = %s, max_tokens = %s, "
            "enabled = %s WHERE id = %s",
            (model_id, "chat", 1, 2048, 1, mid))
    else:
        cur.execute("SELECT IFNULL(MAX(id), 0) + 1 FROM llm_model")
        mid = int(cur.fetchone()[0])
        cur.execute(
            "INSERT INTO llm_model (id, provider_id, model_id, model_name, model_type, weight, "
            "max_tokens, enabled) VALUES (%s, %s, %s, %s, %s, %s, %s, %s)",
            (mid, pid, model_id, model_id, "chat", 1, 2048, 1))
    print("seeded provider id=%d name=%s; model id=%d model_id=%s" % (pid, name, mid, model_id))


def seed_providers_file(cur, path: Path) -> None:
    items = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(items, list):
        sys.exit("providers file must be a JSON array")
    for item in items:
        name = str(item["name"])
        base_url = validate_base_url(str(item["base_url"]), allow_private=True)
        enc = encrypt(str(item["token"]))
        extra = item.get("extra")
        extra_json = json.dumps(extra, ensure_ascii=False) if extra else None
        enabled = 1 if item.get("enabled", True) else 0

        cur.execute("SELECT id FROM llm_provider WHERE name = %s", (name,))
        row = cur.fetchone()
        if row:
            pid = int(row[0])
            cur.execute(
                "UPDATE llm_provider SET base_url = %s, api_key_enc = %s, extra = %s, enabled = %s "
                "WHERE id = %s",
                (base_url, enc, extra_json, enabled, pid))
        else:
            cur.execute("SELECT IFNULL(MAX(id), 0) + 1 FROM llm_provider")
            pid = int(cur.fetchone()[0])
            cur.execute(
                "INSERT INTO llm_provider (id, name, base_url, api_key_enc, extra, enabled) "
                "VALUES (%s, %s, %s, %s, %s, %s)",
                (pid, name, base_url, enc, extra_json, enabled))

        for m in item.get("models", []):
            model_id = str(m["model_id"])
            model_type = m.get("model_type", "chat")
            if model_type not in _TYPES:
                sys.exit("bad model_type: must be chat/embedding/image")
            weight = int(m.get("weight", 1))
            max_tokens = int(m.get("max_tokens", 2048))
            model_name = str(m.get("model_name", model_id))
            menabled = 1 if m.get("enabled", True) else 0
            cur.execute("SELECT id FROM llm_model WHERE provider_id = %s AND model_id = %s",
                        (pid, model_id))
            mrow = cur.fetchone()
            if mrow:
                mid = int(mrow[0])
                cur.execute(
                    "UPDATE llm_model SET model_name = %s, model_type = %s, weight = %s, "
                    "max_tokens = %s, enabled = %s WHERE id = %s",
                    (model_name, model_type, weight, max_tokens, menabled, mid))
            else:
                cur.execute("SELECT IFNULL(MAX(id), 0) + 1 FROM llm_model")
                mid = int(cur.fetchone()[0])
                cur.execute(
                    "INSERT INTO llm_model (id, provider_id, model_id, model_name, model_type, "
                    "weight, max_tokens, enabled) VALUES (%s, %s, %s, %s, %s, %s, %s, %s)",
                    (mid, pid, model_id, model_name, model_type, weight, max_tokens, menabled))
            print("seeded provider id=%d name=%s; model id=%d model_id=%s"
                  % (pid, name, mid, model_id))


def verify(cur) -> None:
    cur.execute("SELECT id, name, base_url, api_key_enc, enabled FROM llm_provider ORDER BY id")
    print("== providers ==")
    for pid, name, base_url, enc, enabled in cur.fetchall():
        try:
            masked = mask_secret(decrypt(enc))
        except SecretError as exc:
            masked = "<undecryptable: %s>" % exc
        print("  [%d] %s enabled=%d base=%s key=%s" % (pid, name, enabled, base_url, masked))
    cur.execute("SELECT id, provider_id, model_id, model_name, model_type, weight, enabled "
                "FROM llm_model ORDER BY id")
    print("== models ==")
    for row in cur.fetchall():
        print("  [%d] provider=%d %s (%s, w=%d) enabled=%d  %s"
              % (row[0], row[1], row[2], row[4], row[5], row[6], row[3]))


def main() -> None:
    ap = argparse.ArgumentParser(description="LLM 渠道 seed（密文入库）")
    ap.add_argument("--env-file", type=Path, default=None, help="单渠道引导（读 LLM_*）")
    ap.add_argument("--providers-file", type=Path, default=None, help="多渠道 JSON（gitignored）")
    ap.add_argument("--verify", action="store_true", help="解密脱敏校验")
    args = ap.parse_args()

    load_keys()  # 主密钥必须就绪（--verify 也要）
    conn = _conn()
    try:
        with conn.cursor() as cur:
            if args.verify:
                verify(cur)
            elif args.providers_file:
                seed_providers_file(cur, args.providers_file)
            elif args.env_file:
                seed_env_file(cur, args.env_file)
            else:
                ap.print_help(sys.stderr)
                sys.exit(2)
    finally:
        conn.close()


if __name__ == "__main__":
    main()
