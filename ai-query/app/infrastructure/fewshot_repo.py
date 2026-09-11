"""fewshot_sample 仓库与检索器（B27 数据飞轮，ADR-042 配套）。

- 检索默认关键词重合度（字符二元组 Jaccard）；embedding_ref 列为向量库升级预留；
- 全部方法 best-effort：表未建/DB 不可达静默降级（飞轮失效不影响 NL→条件主链路）；
- 安全口径：全部参数化查询（%s 占位，禁拼接）；condition_json 仅存白名单规范化 JSON。
"""
from __future__ import annotations

import asyncio
import time
from typing import Any

import aiomysql

LIST_TTL_SECONDS = 30.0
_VALID_STATUS = ("ACTIVE", "PENDING", "REJECTED")


def _bigrams(text: str) -> set[str]:
    t = "".join(ch for ch in text.lower() if ch.isalnum())
    if not t:
        return set()
    grams = {t[i:i + 2] for i in range(len(t) - 1)}
    return grams or {t}


def select_top(question: str, samples: list[dict[str, Any]], k: int = 3) -> list[dict[str, Any]]:
    """关键词重合度检索 top-k（字符二元组 Jaccard；零分不入选）。"""
    qg = _bigrams(question)
    if not qg:
        return []
    scored: list[tuple[float, dict[str, Any]]] = []
    for s in samples:
        sg = _bigrams(str(s.get("question", "")))
        union = len(qg | sg)
        if not union:
            continue
        score = len(qg & sg) / union
        if score > 0:
            scored.append((score, s))
    scored.sort(key=lambda p: -p[0])
    return [s for _, s in scored[:k]]


class FewshotRepo:
    """fewshot_sample 访问 + 进程内 30s 列表缓存；pool=None（DB 未配）即整仓降级。"""

    def __init__(self, pool: aiomysql.Pool | None = None, ttl: float = LIST_TTL_SECONDS) -> None:
        self._pool = pool
        self._ttl = ttl
        self._cache: tuple[float, list[dict[str, Any]]] | None = None
        self._lock = asyncio.Lock()

    def invalidate(self) -> None:
        self._cache = None

    async def list_active(self) -> list[dict[str, Any]]:
        async with self._lock:
            if self._cache and time.monotonic() - self._cache[0] < self._ttl:
                return self._cache[1]
            rows: list[dict[str, Any]] = []
            if self._pool is not None:
                try:
                    async with self._pool.acquire() as conn:
                        async with conn.cursor(aiomysql.DictCursor) as cur:
                            await cur.execute(
                                "SELECT id, question, condition_json, hit_count FROM fewshot_sample "
                                "WHERE status = 'ACTIVE' AND condition_json IS NOT NULL "
                                "ORDER BY hit_count DESC, id LIMIT 500")
                            rows = [dict(r) for r in await cur.fetchall()]
                except Exception:  # noqa: BLE001 表未建/DB 不可达 → 空语料降级
                    rows = []
            self._cache = (time.monotonic(), rows)
            return rows

    async def top_k(self, question: str, k: int = 3) -> list[dict[str, Any]]:
        return select_top(question, await self.list_active(), k)

    async def record_resolved(self, question: str, condition_json: str) -> None:
        """解析成功（护栏已验证的正样本）：去重 +1；新样本直接 ACTIVE 入语料。"""
        await self._upsert(question, condition_json, "online_hit", "ACTIVE")

    async def record_unknown(self, question: str) -> None:
        """UNKNOWN 拒答回流：新样本 PENDING 待人工标注（condition 为空）。"""
        await self._upsert(question, None, "online_unknown", "PENDING")

    async def _upsert(self, question: str, condition_json: str | None,
                      source: str, status_new: str) -> None:
        if self._pool is None:
            return
        try:
            async with self._pool.acquire() as conn:
                async with conn.cursor() as cur:
                    await cur.execute(
                        "SELECT id FROM fewshot_sample WHERE question = %s ORDER BY id LIMIT 1",
                        (question,))
                    row = await cur.fetchone()
                    if row:
                        await cur.execute(
                            "UPDATE fewshot_sample SET hit_count = hit_count + 1 WHERE id = %s",
                            (row[0],))
                    else:
                        await cur.execute(
                            "INSERT INTO fewshot_sample (question, condition_json, source, status, hit_count) "
                            "VALUES (%s, %s, %s, %s, 1)",
                            (question, condition_json, source, status_new))
        except Exception:  # noqa: BLE001 best-effort，飞轮写入失败不影响主流程
            return

    async def set_status(self, sample_id: int, status: str) -> bool:
        if self._pool is None or status not in _VALID_STATUS:
            return False
        try:
            async with self._pool.acquire() as conn:
                async with conn.cursor() as cur:
                    await cur.execute("UPDATE fewshot_sample SET status = %s WHERE id = %s",
                                      (status, sample_id))
                    self.invalidate()
                    return True
        except Exception:  # noqa: BLE001
            return False

    async def set_condition(self, sample_id: int, condition_json: str) -> bool:
        if self._pool is None:
            return False
        try:
            async with self._pool.acquire() as conn:
                async with conn.cursor() as cur:
                    await cur.execute(
                        "UPDATE fewshot_sample SET condition_json = %s, status = 'PENDING' WHERE id = %s",
                        (condition_json, sample_id))
                    self.invalidate()
                    return True
        except Exception:  # noqa: BLE001
            return False

    async def list_page(self, status: str | None = None, limit: int = 100) -> list[dict[str, Any]]:
        if self._pool is None:
            return []
        try:
            async with self._pool.acquire() as conn:
                async with conn.cursor(aiomysql.DictCursor) as cur:
                    if status:
                        await cur.execute(
                            "SELECT id, question, condition_json, source, status, hit_count, created_at "
                            "FROM fewshot_sample WHERE status = %s ORDER BY id DESC LIMIT %s",
                            (status, int(limit)))
                    else:
                        await cur.execute(
                            "SELECT id, question, condition_json, source, status, hit_count, created_at "
                            "FROM fewshot_sample ORDER BY id DESC LIMIT %s", (int(limit),))
                    return [dict(r) for r in await cur.fetchall()]
        except Exception:  # noqa: BLE001
            return []

    async def stats(self) -> dict[str, int]:
        if self._pool is None:
            return {}
        try:
            async with self._pool.acquire() as conn:
                async with conn.cursor() as cur:
                    await cur.execute(
                        "SELECT status, source, COUNT(*) FROM fewshot_sample "
                        "GROUP BY status, source")
                    rows = await cur.fetchall()
            return {f"{status}:{source}": int(n) for status, source, n in rows}
        except Exception:  # noqa: BLE001
            return {}
