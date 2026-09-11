#!/usr/bin/env python3
"""T1 日频指标批量评估引擎（Branch 14，docs/10 §11.7）。

收盘后对 INDICATOR 类规则批量评估：从 stock_daily 宽表拉日线 → 判定 → 命中即产出
冻结契约 alert_event（protobuf 手工编码）→ Kafka alert_event → notify/WS 渠道层。

支持 12 种 T1 类型（docs/10 §11.7）：VOL_RATIO/VOL_DRY/NEW_HIGH/NEW_LOW/MA_CROSS(golden|dead)/
MA_REL(cross_up|cross_down)/CONSEC_UP/CONSEC_DOWN/BOLL(upper|lower)/RSI/MACD。

用法：
  py -3 scripts/daily/evaluate_indicators.py --rules 901,902
环境：
  HQ_CLICKHOUSE_URL（默认 http://127.0.0.1:23004）、HQ_CLICKHOUSE_USER/PASSWORD、
  EVAL_KAFKA（默认 127.0.0.1:23003）、EVAL_MYSQL_DSN
"""
import argparse
import json
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "daily"))
from netguard import local_ck_query  # noqa: E402

import pymysql  # noqa: E402

RULES_SQL = ("SELECT id, version, market, symbol, `condition` FROM alert_rule"
             " WHERE rule_type = 'INDICATOR' AND status = 1")

BARS_SQL = ("SELECT date, open, high, low, close, pre_close, pct_chg, volume, amount"
            " FROM stock_daily WHERE market='A_SHARE' AND symbol={sym:String}"
            " AND date >= {d0:Date} AND date <= {d1:Date} ORDER BY date")


def fetch_bars(ck, ck_user, ck_pass, symbol, window):
    """近 window 自然日日线（升序）。"""
    import datetime
    d1 = datetime.date.today().isoformat()
    d0 = (datetime.date.today() - datetime.timedelta(days=window + 5)).isoformat()
    rows = local_ck_query(ck, {
        "query": BARS_SQL,
        "param_sym": symbol,
        "param_d0": d0,
        "param_d1": d1,
    })
    bars = []
    prev_close = 0.0
    for r in rows:
        pre = prev_close if prev_close else float(r["pre_close"])
        bars.append({
            "date": r["date"], "open": float(r["open"]), "high": float(r["high"]),
            "low": float(r["low"]), "close": float(r["close"]),
            "pct_chg": float(r["pct_chg"]), "volume": float(r["volume"]),
            "amount": float(r["amount"]), "pre_close": pre,
        })
        prev_close = float(r["close"])
    return bars


# ---- T1 12 种类型判定（docs/10 §11.7） ----

def eval_vol_ratio(bars, ratio):
    if not bars:
        return False, {}
    last = bars[-1]
    vol5 = sum(b["volume"] for b in bars[-6:-1]) / 5 if len(bars) >= 6 else 0
    return vol5 > 0 and last["volume"] / vol5 >= ratio, {"type": "VOL_RATIO", "ratio": round(last["volume"] / vol5, 2) if vol5 else 0}


def eval_vol_dry(bars, ratio):
    if not bars:
        return False, {}
    last = bars[-1]
    vol5 = sum(b["volume"] for b in bars[-6:-1]) / 5 if len(bars) >= 6 else 0
    return vol5 > 0 and last["volume"] / vol5 <= ratio, {"type": "VOL_DRY", "ratio": round(last["volume"] / vol5, 2) if vol5 else 0}


def eval_new_high(bars, period):
    if len(bars) < period:
        return False, {}
    recent = bars[-period:]
    peak = max(b["high"] for b in recent)
    return recent[-1]["close"] >= peak, {"type": "NEW_HIGH", "high": recent[-1]["high"], "period": period}


def eval_new_low(bars, period):
    if len(bars) < period:
        return False, {}
    recent = bars[-period:]
    trough = min(b["low"] for b in recent)
    return recent[-1]["close"] <= trough, {"type": "NEW_LOW", "low": recent[-1]["low"], "period": period}


def eval_ma_cross(bars, fast, slow):
    if len(bars) < slow + 1:
        return False, {}
    closes = [b["close"] for b in bars]
    ma = lambda xs, n, end: sum(xs[end - n:end]) / n  # noqa: E731
    f_prev = ma(closes, fast, len(closes) - 1)
    s_prev = ma(closes, slow, len(closes) - 1)
    f_now = ma(closes, fast, len(closes))
    s_now = ma(closes, slow, len(closes))
    golden = f_prev <= s_prev and f_now > s_now
    dead = f_prev >= s_prev and f_now < s_now
    if golden:
        return True, {"type": "MA_CROSS", "direction": "golden", "fast": round(f_now, 2), "slow": round(s_now, 2)}
    if dead:
        return True, {"type": "MA_CROSS", "direction": "dead", "fast": round(f_now, 2), "slow": round(s_now, 2)}
    return False, {}


def eval_consec_up(bars, days):
    if len(bars) < days:
        return False, {}
    window = bars[-days:]
    if all(b["close"] > b["open"] for b in window):
        return True, {"type": "CONSEC_UP", "days": days}
    return False, {}


def eval_consec_down(bars, days):
    if len(bars) < days:
        return False, {}
    window = bars[-days:]
    if all(b["close"] < b["open"] for b in window):
        return True, {"type": "CONSEC_DOWN", "days": days}
    return False, {}


def eval_boll_touch(bars, period, band):
    if len(bars) < period:
        return False, {}
    closes = [b["close"] for b in bars[-period:]]
    mid = sum(closes) / period
    sq = sum((c - mid) ** 2 for c in closes) / period
    sd = sq ** 0.5
    upper = mid + 2 * sd
    lower = mid - 2 * sd
    last = bars[-1]
    if band == "upper":
        return last["close"] >= upper, {"type": "BOLL_UPPER", "upper": round(upper, 2)}
    if band == "lower":
        return last["close"] <= lower, {"type": "BOLL_LOWER", "lower": round(lower, 2)}
    return False, {}


def eval_rsi_extreme(bars, period, threshold, direction):
    if len(bars) < period + 1:
        return False, {}
    gains, losses = [], []
    for i in range(1, len(bars)):
        d = bars[i]["close"] - bars[i - 1]["close"]
        gains.append(max(d, 0))
        losses.append(max(-d, 0))
    gains = gains[-period:]
    losses = losses[-period:]
    avg_gain = sum(gains) / period
    avg_loss = sum(losses) / period
    if avg_loss == 0:
        rsi = 100 if avg_gain > 0 else 50
    else:
        rs = avg_gain / avg_loss
        rsi = 100 - 100 / (1 + rs)
    if direction == "overbought":
        return rsi >= threshold, {"type": "RSI_OVERBOUGHT", "rsi": round(rsi, 1)}
    if direction == "oversold":
        return rsi <= threshold, {"type": "RSI_OVERSOLD", "rsi": round(rsi, 1)}
    return False, {}


def eval_macd_golden(bars):
    if len(bars) < 26 + 9:
        return False, {}
    closes = [b["close"] for b in bars]
    ema12 = _ema(closes, 12)
    ema26 = _ema(closes, 26)
    dif = [a - b for a, b in zip(ema12, ema26)]
    dea = _ema(dif, 9)
    n = len(dif) - 1
    golden = dif[n - 1] <= dea[n - 1] and dif[n] > dea[n]
    return golden, {"type": "MACD_GOLDEN"}


def _ema(xs, n):
    k = 2 / (n + 1)
    out = [xs[0]]
    for v in xs[1:]:
        out.append(out[-1] * (1 - k) + v * k)
    return out
