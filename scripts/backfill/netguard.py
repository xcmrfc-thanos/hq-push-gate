#!/usr/bin/env python3
"""回填/对账脚本的出网与本地写入边界（docs/09 §5.1 Branch 4 配套）。

- external_get_json：仅 https + 域名白名单 + 解析 IP 阻断私网/环回/链路本地/保留段 + 禁重定向，
  防 SSRF 与 DNS rebinding（解析后地址逐次校验）。
- local_ck_insert：ClickHouse 本地写入，仅放行环回地址（localhost/127.0.0.1/::1），
  SQL 文本静态、数据走 JSONEachRow body，无拼接注入面。
"""
import ipaddress
import json
import os
import socket
import urllib.parse
import urllib.request

USER_AGENT = "hq-push-gate-backfill/0.1"
LOCAL_HOSTS = {"localhost", "127.0.0.1", "::1"}


def _ck_auth_params() -> dict[str, str]:
    """ClickHouse HTTP 认证参数（开发凭据，与 compose CLICKHOUSE_USER/PASSWORD 对齐）。"""
    return {
        "user": os.environ.get("HQ_CLICKHOUSE_USER", "hqpush"),
        "password": os.environ.get("HQ_CLICKHOUSE_PASSWORD", "hqpush"),
    }


class SecurityError(ValueError):
    """出网/写入边界校验失败。"""


def _assert_resolved_safe(hostname: str, *, allow_local: bool) -> None:
    infos = socket.getaddrinfo(hostname, None)
    for info in infos:
        ip = ipaddress.ip_address(info[4][0])
        if not allow_local and (
            ip.is_private or ip.is_loopback or ip.is_link_local or ip.is_reserved
        ):
            raise SecurityError(f"{hostname} resolves to non-public {ip}")
        if allow_local and not ip.is_loopback:
            raise SecurityError(f"local writer: {hostname} must be loopback, got {ip}")


def external_get_json(url: str, allowed_hosts: set[str], timeout: int = 10) -> dict:
    parsed = urllib.parse.urlparse(url)
    if parsed.scheme != "https" or parsed.hostname not in allowed_hosts:
        raise SecurityError(f"blocked url: scheme={parsed.scheme} host={parsed.hostname}")
    _assert_resolved_safe(parsed.hostname, allow_local=False)

    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *args, **kwargs):  # noqa: ANN002, ANN003
            return None

    opener = urllib.request.build_opener(NoRedirect)
    req = urllib.request.Request(url, headers={"User-Agent": USER_AGENT})
    with opener.open(req, timeout=timeout) as resp:
        return json.loads(resp.read().decode("utf-8"))


def external_get_json_multi(urls: list[str], allowed_hosts: set[str], timeout: int = 10) -> dict:
    """同一路径多镜像轮换（东财主域夜间维护/限流时切备用域），全部失败才抛错。"""
    last_err: Exception = SecurityError("no urls")
    for url in urls:
        try:
            return external_get_json(url, allowed_hosts, timeout)
        except Exception as exc:  # noqa: BLE001 换下一个镜像
            last_err = exc
    raise last_err


def local_ck_insert(base_url: str, query: str, body: str, timeout: int = 30) -> None:
    parsed = urllib.parse.urlparse(base_url)
    if parsed.scheme not in ("http", "https") or parsed.hostname.lower() not in LOCAL_HOSTS:
        raise SecurityError(f"local writer blocked: {base_url}")
    _assert_resolved_safe(parsed.hostname, allow_local=True)
    params = {"query": query, **_ck_auth_params()}
    url = base_url.rstrip("/") + "/?" + urllib.parse.urlencode(params)
    req = urllib.request.Request(
        url, data=body.encode("utf-8"),
        headers={"Content-Type": "text/plain"}, method="POST",
    )
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        resp.read()


def local_ck_query(base_url: str, params: dict[str, str], timeout: int = 30):
    """ClickHouse 本地只读查询：{name:Type} 参数占位经 URL 下发（无拼接注入面）。"""
    parsed = urllib.parse.urlparse(base_url)
    if parsed.scheme not in ("http", "https") or parsed.hostname.lower() not in LOCAL_HOSTS:
        raise SecurityError(f"local reader blocked: {base_url}")
    _assert_resolved_safe(parsed.hostname, allow_local=True)
    params = {**params, **_ck_auth_params()}
    url = base_url.rstrip("/") + "/?" + urllib.parse.urlencode(params)
    with urllib.request.urlopen(url, timeout=timeout) as resp:
        return json.loads(resp.read().decode("utf-8"))
