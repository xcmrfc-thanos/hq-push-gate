"""白名单条件模型 v2（domain 层，ADR-042：以 docs/10 §11 为权威 schema 的三方对齐版）。

v2 变更（B25）：
- 公共叠加字段 once/session/expire_at：None=未指定（dump 时省略，旧 JSON 形态保持兼容），
  执行端默认语义见 docs/10 §11.0（Flink 实时路径当前未强制，属登记项）；
- PCT_CHANGE 规范形 = 正幅值 threshold + direction(up/down/both)；解析层接受负数输入并归一化
  （负值 ≡ direction=down），保证下发的条件永远与 Flink matchPctChange（幅值+方向）语义一致；
- limit=true（涨跌停预警）要求显式 direction ∈ {up,down}（Flink limit 分支无 both 语义）；
- ConditionGroup(all/any) 接入 parse_condition：递归深度限 2、单组 1~10 项；
  组合仅查询侧使用（规则侧扁平，RuleConditions.match 不解释 all/any）。

安全口径不变：LLM 输出必须落在本模块结构内才能进入查询（防提示注入 → SQL 注入的传导）；
数值字段限 float 且必须有界；查询 SQL 由「类型 → 固片段 + {name:Type} 占位」映射生成，
用户文本永不进入 SQL。
"""
from __future__ import annotations

import hashlib
import json
from typing import Literal, Union

from pydantic import BaseModel, Field, field_validator, model_validator

# 数值边界：防止 NaN/极端值进入 SQL 与无意义查询
MAX_PRICE = 1_000_000.0
MAX_PCT = 100.0
MAX_VOLUME = 1e12

Direction = Literal["up", "down", "both"]
Session = Literal["open", "close", "all"]


class _Overlay(BaseModel):
    """公共叠加字段（docs/10 §11.0，所有类型可叠加；None=未指定，下游按默认执行）。"""

    once: bool | None = None
    session: Session | None = None
    expire_at: int | None = Field(default=None, ge=1)  # 毫秒时间戳


class PriceAbove(_Overlay):
    type: Literal["PRICE_ABOVE"] = "PRICE_ABOVE"
    threshold: float = Field(gt=0, le=MAX_PRICE)


class PriceBelow(_Overlay):
    type: Literal["PRICE_BELOW"] = "PRICE_BELOW"
    threshold: float = Field(gt=0, le=MAX_PRICE)


class PriceRange(_Overlay):
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


class PctChange(_Overlay):
    type: Literal["PCT_CHANGE"] = "PCT_CHANGE"
    # 规范形恒为正幅值（|threshold| ∈ (0, 100]）；负数输入由 _canonicalize 归一化为 down
    threshold: float = Field(le=MAX_PCT)
    direction: Direction | None = None
    limit: bool | None = None  # 涨跌停预警：触发线由 Flink 按板块推导（threshold 为查询筛选幅值）

    @field_validator("threshold")
    @classmethod
    def _bounded_nonzero(cls, v: float) -> float:
        if not 0 < abs(v) <= MAX_PCT:
            raise ValueError("threshold must be non-zero with |threshold| <= 100")
        return v

    @model_validator(mode="after")
    def _canonicalize(self) -> "PctChange":
        # 负值 ≡ 跌幅别名：归一化为正幅值 + direction=down（与 Flink 幅值+方向语义对齐）
        if self.threshold < 0:
            if self.direction == "up":
                raise ValueError("negative threshold conflicts with direction='up'")
            self.direction = "down"
            self.threshold = abs(self.threshold)
        if self.limit and self.direction in (None, "both"):
            raise ValueError("limit=true requires explicit direction 'up' or 'down'")
        return self


class VolumeAbove(_Overlay):
    type: Literal["VOLUME_ABOVE"] = "VOLUME_ABOVE"
    threshold: float = Field(gt=0, le=MAX_VOLUME)
    # 规则侧映射（B26 订阅转换用）：ruleType=INDICATOR, condition={"indicator":"VOLUME","op":">","value":threshold}


Condition = Union[PriceAbove, PriceBelow, PriceRange, PctChange, VolumeAbove]


class ConditionGroup(BaseModel):
    """组合条件树：all/any 二选一；子项为叶子或再嵌一层组合（深度限制在 parse_condition 强制）。"""

    all: list["ConditionNode"] | None = None
    any: list["ConditionNode"] | None = None  # noqa: PIE796


ConditionNode = Union[Condition, ConditionGroup]
ConditionGroup.model_rebuild()

_LEAF_MODELS = {
    "PRICE_ABOVE": PriceAbove,
    "PRICE_BELOW": PriceBelow,
    "PRICE_RANGE": PriceRange,
    "PCT_CHANGE": PctChange,
    "VOLUME_ABOVE": VolumeAbove,
}


def parse_condition(raw: str | dict) -> Condition | ConditionGroup:
    """解析并校验 LLM 输出为白名单条件/组合树；任何越界形状抛 ValueError。"""
    if isinstance(raw, str):
        raw = _extract_json(raw)
    if not isinstance(raw, dict):
        raise ValueError("condition must be a JSON object")
    return _parse_node(raw, depth=1)


def _parse_node(raw: dict, depth: int) -> ConditionNode:
    if "type" in raw and ("all" in raw or "any" in raw):
        raise ValueError("ambiguous condition: both 'type' and group keys ('all'/'any')")
    if "type" in raw:
        return _parse_leaf(raw)
    if "all" in raw or "any" in raw:
        keys = [k for k in ("all", "any") if k in raw]
        if len(keys) != 1:
            raise ValueError("group must specify exactly one of 'all'/'any'")
        if depth > 2:
            raise ValueError("condition group nesting exceeds 2 levels")
        items = raw[keys[0]]
        if not isinstance(items, list) or not 1 <= len(items) <= 10:
            raise ValueError("group must be a list of 1..10 conditions")
        children = [_parse_node(item, depth + 1) for item in items]
        return ConditionGroup(**{keys[0]: children})
    raise ValueError("condition must contain 'type' or 'all'/'any'")


def _parse_leaf(raw: dict) -> Condition:
    ctype = raw.get("type")
    # indicator 类条件查询侧无计算源，显式拒绝（规则侧 INDICATOR/VOLUME 走订阅转换，B26）
    if ctype in ("UNKNOWN", "INDICATOR") or ctype is None:
        raise ValueError("unsupported or unknown condition type")
    model = _LEAF_MODELS.get(ctype)
    if model is None:
        raise ValueError(f"unknown condition type: {ctype!r}")
    return model.model_validate(raw)


def cache_key(question: str) -> str:
    """aicond:v2:{sha256(question)}（v2：schema 对齐后与 v1 语义隔离，ADR-042）。"""
    return "aicond:v2:" + hashlib.sha256(question.strip().encode("utf-8")).hexdigest()


def to_json(cond: Condition | ConditionGroup) -> str:
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
