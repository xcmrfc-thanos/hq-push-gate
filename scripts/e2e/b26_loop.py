#!/usr/bin/env python3
"""B26/B27 联调实测脚本（Windows shell 中文编码安全）：
1) 登录取 JWT；2) NL→ai/query（真实 LLM 渠道）；3) 单票订阅（condition 透传 /rules）；
4) 轮询 /api/v1/alerts 观察 Flink 命中→notify 投递；5) ACK。
用法：py -3 scripts/e2e/b26_loop.py [--q 条件] [--symbol 600000] [--wait 40]

安全口径：目标主机白名单锁定 127.0.0.1 联调面（见 _guard），仅本机 dev 环境使用。
"""
from __future__ import annotations

import argparse
import ipaddress
import json
import socket
import time
import urllib.parse
import urllib.request

_ALLOWED_HOSTS = {"127.0.0.1", "localhost"}
_BIZ = "http://127.0.0.1:23026"
_AI = "http://127.0.0.1:23041"
_USER, _PASSWORD = "e2e-b26", "e2e-Pass-2026"


def _guard(url: str) -> str:
    """SSRF 边界：仅 http 协议 + 白名单主机 + 解析 IP 不得为公网/链路本地。"""
    p = urllib.parse.urlsplit(url)
    if p.scheme != "http" or p.hostname not in _ALLOWED_HOSTS:
        raise ValueError(f"host not allowed: {p.hostname}")
    for fam, _, _, _, sa in socket.getaddrinfo(p.hostname, p.port or 80):
        ip = ipaddress.ip_address(sa[0])
        if fam == socket.AF_INET and not ip.is_loopback and not ip.is_private:
            raise ValueError(f"resolved public ip rejected: {ip}")
    return url


def post(url: str, body: dict, token: str | None = None) -> dict:
    req = urllib.request.Request(
        _guard(url), data=json.dumps(body).encode("utf-8"), method="POST",
        headers={"Content-Type": "application/json",
                 **({"Authorization": f"Bearer {token}"} if token else {})})
    with urllib.request.urlopen(req, timeout=60) as r:
        return json.loads(r.read().decode("utf-8"))


def get(url: str, token: str | None = None) -> dict:
    req = urllib.request.Request(_guard(url),
                                 headers={"Authorization": f"Bearer {token}"} if token else {})
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.loads(r.read().decode("utf-8"))


def condition_to_rule_input(condition: dict, market: str, symbol: str,
                            cooldown: int = 60) -> tuple[bool, object]:
    """ADR-042 §8 订阅映射（与 packages/shared conditionToRuleInput 同口径）。"""
    ctype = condition.get("type")
    if not isinstance(ctype, str):
        return False, "条件缺少 type"
    if "all" in condition or "any" in condition:
        return False, "组合条件暂不支持订阅"
    rule_cond = {k: v for k, v in condition.items() if k != "type"}
    rule_type = ctype
    if ctype == "VOLUME_ABOVE":
        t = rule_cond.pop("threshold", None)
        if not isinstance(t, (int, float)) or t <= 0:
            return False, "成交量阈值非法"
        rule_type = "INDICATOR"
        rule_cond.update({"indicator": "VOLUME", "op": ">", "value": t})
    return True, {"market": market, "symbol": symbol, "ruleType": rule_type,
                  "condition": json.dumps(rule_cond), "cooldownSec": cooldown, "status": 1}


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--q", default="股价大于1元的股票")
    ap.add_argument("--symbol", default="600000")
    ap.add_argument("--wait", type=int, default=60)
    args = ap.parse_args()

    login = post(f"{_BIZ}/api/v1/auth/login", {"username": _USER, "password": _PASSWORD})
    token = login["data"]["access_token"]
    print(f"[1] login ok uid={login['data']['user_id']}")

    qr = post(f"{_AI}/api/v1/ai/query", {"q": args.q, "limit": 10}, token)
    data = qr["data"]
    print(f"[2] ai/query ok cached={data.get('cached')} version={data.get('condition_version')}")
    print(f"    condition = {json.dumps(data['condition'], ensure_ascii=False)}")
    print(f"    symbols   = {[s['symbol'] for s in data['symbols']][:10]}")

    ok, rule_input = condition_to_rule_input(data["condition"], "A_SHARE", args.symbol)
    if not ok:
        print(f"[3] 订阅映射拒绝：{rule_input}")
        return 2
    rule = post(f"{_BIZ}/api/v1/rules", rule_input, token)["data"]
    print(f"[3] 订阅 ok rule_id={rule['id']} ruleType={rule['ruleType']} condition={rule['condition']}")

    print(f"[4] 轮询 /alerts 等待 Flink 命中→notify 投递（≤{args.wait}s）…")
    deadline = time.time() + args.wait
    while time.time() < deadline:
        pulled = get(f"{_BIZ}/api/v1/alerts?cursor=0&limit=50", token)
        hit = next((a for a in pulled["data"]["items"]
                    if a.get("rule_id") == rule["id"]), None)
        if hit:
            print(f"[4] 命中→投递收到：delivery_id={hit.get('delivery_id')} title={hit.get('title')}")
            ack = post(f"{_BIZ}/api/v1/alerts/ack", {"delivery_id": hit.get("delivery_id")}, token)
            print(f"[5] ACK ok updated={ack['data'].get('updated')}")
            return 0
        time.sleep(3)
    print("[4] 超时未观察到投递（检查 Flink job / notify 日志）")
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
