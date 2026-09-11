"""B27 数据飞轮单测：关键词检索排序、服务回写（成功→正样本/UNKNOWN→待标注）、降级路径。

repo 的 SQL 分支为 best-effort 薄封装（参数化查询），SQL 形状由开发环境冒烟验证；
单测范围 = 检索排序 + 服务集成 + 无池降级。
"""
from __future__ import annotations

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))  # ai-query/ 进入 path

from app.application.nl2cond import AIQueryService
from app.infrastructure.fewshot_repo import FewshotRepo, select_top

from app.tests.test_condition import FakeCK, FakeCache, FakeLLM, _run


def test_select_top_ranks_by_overlap():
    samples = [
        {"id": 1, "question": "股价大于10元", "condition_json": '{"type":"PRICE_ABOVE","threshold":10}'},
        {"id": 2, "question": "跌幅达到3%的股票", "condition_json": '{"type":"PCT_CHANGE","threshold":3,"direction":"down"}'},
        {"id": 3, "question": "涨幅超过5%的股票", "condition_json": '{"type":"PCT_CHANGE","threshold":5}'},
        {"id": 4, "question": "成交量超过100万股", "condition_json": '{"type":"VOLUME_ABOVE","threshold":1000000}'},
    ]
    top = select_top("涨幅超过8%的股票", samples, k=2)
    assert len(top) == 2
    assert top[0]["id"] == 3  # 与"涨幅…%"重合度最高


def test_select_top_no_match_returns_empty():
    samples = [{"id": 1, "question": "股价大于10元", "condition_json": "{}"}]
    assert select_top("完全不同的另外一句话", samples, k=3) == []
    assert select_top("", samples, k=3) == []


class FakeFewshots:
    """行为替身：记录调用；top_k 返回预置样本。"""

    def __init__(self, shots=None) -> None:
        self.shots = shots or []
        self.resolved: list[tuple[str, str]] = []
        self.unknown: list[str] = []

    async def top_k(self, question: str, k: int = 3):
        return self.shots

    async def record_resolved(self, question: str, condition_json: str) -> None:
        self.resolved.append((question, condition_json))

    async def record_unknown(self, question: str) -> None:
        self.unknown.append(question)


def test_service_records_resolved_with_canonical_json():
    fs = FakeFewshots()
    svc = AIQueryService(FakeLLM('{"type":"PRICE_ABOVE","threshold":100}'), FakeCK(), FakeCache(), fewshots=fs)
    _run(svc.query("价格超过100元的股票"))
    assert len(fs.resolved) == 1
    q, cond_json = fs.resolved[0]
    assert q == "价格超过100元的股票"
    assert '"type":"PRICE_ABOVE"' in cond_json and '"threshold":100.0' in cond_json


def test_service_records_unknown():
    import pytest

    from app.application.nl2cond import UnknownCondition

    fs = FakeFewshots()
    svc = AIQueryService(FakeLLM('{"type":"UNKNOWN"}'), FakeCK(), FakeCache(), fewshots=fs)
    with pytest.raises(UnknownCondition):
        _run(svc.query("优质白马股推荐"))
    assert fs.unknown == ["优质白马股推荐"]
    assert fs.resolved == []


def test_service_injects_similar_shots_into_prompt():
    class SystemCapturingLLM:
        """记录 system 消息（shots 注入点在 system，FakeLLM 只记 user）。"""

        def __init__(self, reply: str) -> None:
            self.reply = reply
            self.systems: list[str] = []

        async def complete(self, system: str, user: str, max_tokens: int = 300, client_ip=None):
            self.systems.append(system)
            return self.reply, "fake"

    shots = [{"id": 1, "question": "涨幅超过5%的股票", "condition_json": '{"type":"PCT_CHANGE","threshold":5}'}]
    llm = SystemCapturingLLM('{"type":"PCT_CHANGE","threshold":5,"direction":"up"}')
    svc = AIQueryService(llm, FakeCK(), FakeCache(), fewshots=FakeFewshots(shots))
    _run(svc.query("只要涨的：涨幅超过5%"))
    assert "相似问法参考" in llm.systems[0]
    assert '"type":"PCT_CHANGE","threshold":5}' in llm.systems[0]


def test_repo_degrades_without_pool():
    repo = FewshotRepo(pool=None)
    assert _run(repo.list_active()) == []
    assert _run(repo.top_k("anything")) == []
    assert _run(repo.stats()) == {}
    assert _run(repo.set_status(1, "ACTIVE")) is False
    assert _run(repo.set_condition(1, "{}")) is False
    assert _run(repo.list_page()) == []
