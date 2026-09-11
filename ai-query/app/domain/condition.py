"""白名单条件模型（domain 层，docs/08 §4.3 / docs/04 §1 冻结 RuleType 子集）。

LLM 输出必须落在本模块定义的结构内才能进入查询（防提示注入 → SQL 注入的传导）：
- 只接受 5 种条件类型；数值字段限 float 且必须有界；
- 支持组合条件树 {"all":[...]} / {"any":[...]}（递归校验，限 2 层，docs/10 §11 选股演进第 2 步）；
- 查询 SQL 由「类型 → 固片段 + {name:Type} 占位」映射生成，用户文本永不进入 SQL。
"""
from __future__ import annotations

import hashlib
import json
from typing import Literal, Union

from pydantic import BaseModel, Field, field_validator

CondType = Literal[
    "PRICE_ABOVE", "PRICE_BELOW", "PRICE_RANGE", "PCT_CHANGE", "VOLUME_ABOVE"
]

# 数值边界：防止 NaN/极端值进入 SQL 与无意义查询
MAX_PRICE = 1_000_000.0
MAX_PCT = 100.0
MAX_VOLUME = 1e12


class PriceAbove(BaseModel):
    type: Literal["PRICE_ABOVE"] = "PRICE_ABOVE"
    threshold: float = Field(gt=0, le=MAX_PRICE)


class PriceBelow(BaseModel):
    type: Literal["PRICE_BELOW"] = "PRICE_BELOW"
    threshold: float = Field(gt=0, le=MAX_PRICE)


class PriceRange(BaseModel):
    type: Literal["PRICE_RANGE"] = "PRICE_RANGE"
    low: float = Field(gt=0, le=MAX_PRICE)
    high: float = Field(gt=0, le=MAX_PRICE)

    @field_validator("high")
    @classmethod
    def _high_gt_low(cls, v: float, info) -> float:
        low = info.data.get("low")
        if low is not None and v <= low:
            raise ValueError("high must be greater than low")
        return v


class PctChange(BaseModel):
    type: Literal["PCT_CHANGE"] = "PCT_CHANGE"
    threshold: float = Field(gt=0, le=MAX_PCT)  # 涨跌幅绝对值，百分比


class VolumeAbove(BaseModel):
    type: Literal["VOLUME_ABOVE"] = "VOLUME_ABOVE"
    threshold: float = Field(gt=0, le=MAX_VOLUME)


Condition = Union[PriceAbove, PriceBelow, PriceRange, PctChange, VolumeAbove]


class ConditionGroup(BaseModel):
    """组合条件树：all/any 两种逻辑组合，递归深度限 2 层（docs/10 §11 选股演进）。"""

    all: list | None = None
    any: list | None = None  # noqa: PIE796

    @field_validator("all", "any")
    @classmethod
    def _check_depth(cls, v, info):
        if v is not None and info.field_name == "all" and len(v) > 10:
            raise ValueError("all group max 10 items")
        if v is not None and info.field_name == "any" and len(v) > 10:
            raise ValueError("any group max 10 items")
        return v


def parse_condition(raw: str | dict) -> Condition:
    """解析并校验 LLM 输出为白名单条件；任何越界形状抛 ValueError。"""
    if isinstance(raw, str):
        raw = _extract_json(raw)
    if not isinstance(raw, dict) or "type" not in raw:
        raise ValueError("condition must be an object with 'type'")
    ctype = raw.get("type")
    # indicator 类条件 U0 无计算源，显式拒绝
    if ctype in ("UNKNOWN", "INDICATOR") or ctype is None:
        raise ValueError("unsupported or unknown condition type")
    models = {
        "PRICE_ABOVE": PriceAbove,
        "PRICE_BELOW": PriceBelow,
        "PRICE_RANGE": PriceRange,
        "PCT_CHANGE": PctChange,
        "VOLUME_ABOVE": VolumeAbove,
    }
    model = models.get(ctype)
    if model is None:
        raise ValueError(f"unknown condition type: {ctype!r}")
    return model.model_validate(raw)


def cache_key(question: str) -> str:
    """aicond:{sha256(question)}（docs/04 §5：aicond:{hash}）。"""
    return "aicond:" + hashlib.sha256(question.strip().encode("utf-8")).hexdigest()


def to_json(cond: Condition) -> str:
    return cond.model_dump_json(exclude_none=True)


def _extract_json(text: str) -> dict:
    """从 LLM 文本中容错提取第一个 JSON 对象（忽略围栏/解释性前后缀）。"""
    start = text.find("{")
    if start < 0:
        raise ValueError("no json object in llm output")
    depth = 0
    for i in range(start, len(text)):
        if text[i] == "{":
            depth += 1
        elif text[i] == "}":
            depth -= 1
            if depth == 0:
                return json.loads(text[start : i + 1])
    raise ValueError("unbalanced json in llm output")
