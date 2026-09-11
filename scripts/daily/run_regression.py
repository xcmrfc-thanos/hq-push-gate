#!/usr/bin/env python3
"""NL2Condition 回归 runner（docs/12 B20；branch-queue B5 回归集修复）：
读取 scripts/daily/regression_set.json，逐条用真实 LLM 解析并比对白名单条件。

用法：HQ_MASTER_KEYS=... py -3 scripts/daily/run_regression.py [--channel-env] [--verbose]
默认走 DB 渠道（llm_provider/llm_model）；--channel-env 走 env 单渠道（LLM_*）调试。
退出码：0=全部通过，1=存在失败。零通过阈值可经 LLM_MIN_ACCEPT 调整（默认 0.85）。
"""
from __future__ import annotations

import argparse
import asyncio
import json
import os
import sys
from pathlib import Path

_REPO_ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(_REPO_ROOT / "ai-query"))

from app.application.nl2cond import SYSTEM_PROMPT, UnknownCondition  # noqa: E402
from app.domain.condition import parse_condition, to_json  # noqa: E402
from app.infrastructure.llm import channel_from_env, complete_once  # noqa: E402
from app.infrastructure.llm_gateway import ChannelState, GatewayConfig, LLMGateway, Quota  # noqa: E402
from app.infrastructure.llm_repo import ChannelRepo  # noqa: E402
from app.infrastructure.secrets import load_keys  # noqa: E402

MIN_ACCEPT = float(os.environ.get("LLM_MIN_ACCEPT", "0.85"))


def _channels(use_env: bool) -> ChannelRepo:
    if use_env:
        return ChannelRepo(pool=None, env=os.environ)
    import aiomysql
    from app.main import _mysql_pool_kwargs
    pool = None
    kwargs = _mysql_pool_kwargs()
    if kwargs:
        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)
        pool = loop.run_until_complete(aiomysql.create_pool(minsize=1, maxsize=2, autocommit=True, **kwargs))
        loop.close()
    return ChannelRepo(pool)


async def run(use_env: bool, verbose: bool) -> tuple[list, float]:
    data_path = _REPO_ROOT / "scripts" / "daily" / "regression_set.json"
    cases = json.loads(data_path.read_text(encoding="utf-8"))

    http = __import__("httpx").AsyncClient(timeout=30.0)
    gw = LLMGateway(_channels(use_env), http, ChannelState(None), Quota(None, 0, 0),
                    GatewayConfig(max_attempts=3))
    results: list[dict] = []
    try:
        for c in cases:
            q = c["question"]
            try:
                text, channel = await gw.complete(SYSTEM_PROMPT, q)
                cond = parse_condition(text)
                got = json.loads(to_json(cond))
            except (UnknownCondition, ValueError):
                got = None
            except Exception as exc:  # noqa: BLE001 上游故障按失败计（不影响跑完）
                results.append({"question": q, "error": str(exc)})
                continue
            exp = c.get("expected")
            ok = got == exp
            results.append({"question": q, "expected": exp, "got": got, "ok": ok})
            if verbose:
                print(("✅" if ok else "❌"), q, "->", got)
    finally:
        await http.aclose()
    passed = sum(1 for r in results if r.get("ok"))
    rate = passed / len(results) if results else 0.0
    return results, rate


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--channel-env", action="store_true", help="走 env 单渠道（LLM_*），默认 DB 渠道")
    ap.add_argument("--verbose", action="store_true")
    args = ap.parse_args()

    if not args.channel_env:
        load_keys()  # DB 渠道解密必须
    results, rate = asyncio.run(run(args.channel_env, args.verbose))

    print("\n== NL2Condition 回归汇总 ==")
    for r in results:
        if r.get("error"):
            print("⚠️", r["question"], "上游错误:", r["error"])
        elif not r["ok"]:
            print("❌", r["question"], "期望", r["expected"], "实际", r["got"])
    print(f"通过率 {rate:.0%}（{sum(1 for r in results if r.get('ok'))}/{len(results)}，阈值 {MIN_ACCEPT:.0%}）")
    return 0 if rate >= MIN_ACCEPT else 1


if __name__ == "__main__":
    sys.exit(main())
