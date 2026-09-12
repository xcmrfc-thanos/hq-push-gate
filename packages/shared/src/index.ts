// 公共共享类型与常量（契约对齐 docs/04 §6/§7，冻结项）。

/** 市场（与 hqv1.Market 字符串名一致） */
export const MARKETS = ["A_SHARE", "HK", "US", "CRYPTO"] as const;
export type Market = (typeof MARKETS)[number];

export const MARKET_LABELS: Record<Market, string> = {
  A_SHARE: "A股",
  HK: "港股",
  US: "美股",
  CRYPTO: "加密",
};

/** 统一响应信封（docs/04 §6：{code,msg,data}） */
export interface ApiEnvelope<T> {
  code: number;
  msg: string;
  data?: T;
}

/** 行情快照条目（REST /quote/snapshot 返回 Redis hash 字段） */
export interface QuoteSnapshot {
  last: string | number;
  pct: string | number;
  vol: string | number;
  amount: string | number;
  pre_close: string | number;
  ts: string | number;
}

/** WS 推送条目（docs/04 §7：{symbol,last,pct,vol,ts}） */
export interface QuotePush {
  symbol: string; // "A_SHARE:600000"
  last: number;
  pct: number;
  vol: number;
  ts: number;
}

/** 历史K线条目（REST /quote/kline 返回，docs/04 §6） */
export interface KlineBar {
  t: number; // begin_ts 毫秒
  o: number;
  h: number;
  l: number;
  c: number;
  v: number;
  a?: number;
}

/** WS 告警条目（docs/04 §7：{deliveryId,eventId,ruleId,symbol,title,detail,ts}） */
export interface AlertPush {
  deliveryId: string;
  eventId: string;
  ruleId: number;
  symbol: string;
  title: string;
  detail: string;
  ts: number;
}

/** 补拉条目（notify 内部 API -> biz-service 代理） */
export interface AlertRecord {
  delivery_id: string;
  event_id: string;
  rule_id: number;
  market: string;
  symbol: string;
  title: string;
  trigger_at: string;
  cursor_id: number;
}

/** 预警规则（alert_rule） */
export interface AlertRule {
  id?: number;
  market: string;
  symbol: string;
  ruleType: string;
  condition: string; // JSON 字符串，如 {"threshold":12.5}
  cooldownSec: number;
  status: number;
  version?: number;
}

/** 冻结约定（docs/04 §7） */
export const WS_LIMITS = {
  MAX_SUBS_PER_CONN: 200,
  HEARTBEAT_SEC: 30,
  /** 重连指数退避：1s 起倍增，上限 60s（docs/04 §7-3） */
  RECONNECT_MIN_MS: 1000,
  RECONNECT_MAX_MS: 60000,
} as const;

/** "A_SHARE:600000" -> { market, symbol } */
export function splitSymbol(key: string): { market: string; symbol: string } {
  const i = key.indexOf(":");
  if (i < 0) return { market: "", symbol: key };
  return { market: key.slice(0, i), symbol: key.slice(i + 1) };
}

export function fmtPrice(v: number): string {
  return v >= 100 ? v.toFixed(2) : v.toFixed(4);
}

export function fmtVol(v: number): string {
  if (v >= 1e8) return (v / 1e8).toFixed(2) + "亿";
  if (v >= 1e4) return (v / 1e4).toFixed(2) + "万";
  return v.toFixed(0);
}

// ---- AI 选股（schema v2，ADR-042；meta 契约 = GET /api/v1/ai/meta，docs/04 §6） ----

export interface AIFieldMeta {
  name: string;
  label: string;
  type: "float" | "enum" | "bool";
  gt?: number;
  le?: number;
  /** ne/ale：非零/绝对值上限口径（PCT_CHANGE threshold: ne=0, ale=100） */
  ne?: number;
  ale?: number;
  values?: string[];
  required?: boolean;
}

export interface AITypeMeta {
  type: string;
  label: string;
  /** 订阅转换目标（VOLUME_ABOVE → INDICATOR/VOLUME，ADR-042 §8） */
  rule_type: string;
  fields: AIFieldMeta[];
}

export interface AIMeta {
  condition_version: number;
  groups: { max_depth: number; max_items: number };
  query_types: AITypeMeta[];
}

export interface AIQueryResp {
  condition: Record<string, unknown>;
  symbols: Array<{ symbol: string; last: number; pct: number; volume: number }>;
  cached?: boolean;
  condition_version: number;
}

/** 查询条件 → 规则创建入参（单票订阅，B26）。
 * VOLUME_ABOVE 映射 INDICATOR/{"indicator":"VOLUME","op":">","value":threshold}；
 * 组合条件（all/any）规则侧扁平不支持订阅，返回 ok:false。 */
export function conditionToRuleInput(
  condition: Record<string, unknown>,
  market: string,
  symbol: string,
  cooldownSec = 60,
): { ok: true; rule: AlertRule } | { ok: false; reason: string } {
  const type = condition["type"];
  if (typeof type !== "string") return { ok: false, reason: "条件缺少 type" };
  if ("all" in condition || "any" in condition) {
    return { ok: false, reason: "组合条件暂不支持订阅，请对单一条件创建规则" };
  }
  const ruleCond: Record<string, unknown> = { ...condition };
  delete ruleCond.type;
  let ruleType = type;
  if (type === "VOLUME_ABOVE") {
    const t = ruleCond["threshold"];
    if (typeof t !== "number" || !(t > 0)) return { ok: false, reason: "成交量阈值非法" };
    delete ruleCond.threshold;
    ruleType = "INDICATOR";
    ruleCond["indicator"] = "VOLUME";
    ruleCond["op"] = ">";
    ruleCond["value"] = t;
  }
  return {
    ok: true,
    rule: { market, symbol, ruleType, condition: JSON.stringify(ruleCond), cooldownSec, status: 1 },
  };
}
