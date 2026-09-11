// 内部管理台 API 封装：X-Internal-Token 头 + {code,msg,data} 信封（docs/11 §2）。
// /notify /ai 前缀经 vite proxy 直连内网服务，不经 APISIX。

const TOKEN_KEY = "hq-admin-token";

export function getToken(): string {
  return localStorage.getItem(TOKEN_KEY) ?? "";
}

export function setToken(t: string): void {
  localStorage.setItem(TOKEN_KEY, t);
}

export class ApiError extends Error {
  constructor(
    public code: number,
    msg: string,
    public status: number,
  ) {
    super(msg);
  }
}

async function call<T>(base: string, path: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(base + path, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      "X-Internal-Token": getToken(),
      ...(init?.headers ?? {}),
    },
  });
  const body = await resp.json().catch(() => ({ code: -1, msg: `http ${resp.status}` }));
  if (body.code !== 0) {
    throw new ApiError(body.code, body.msg ?? "unknown", resp.status);
  }
  return body.data as T;
}

export const notifyApi = {
  listChannels: () => call<{ items: ChannelRow[] }>("/notify", "/internal/v1/channels"),
  deliveryStats: () => call<{ rules: DeliveryRuleStat[] }>("/notify", "/internal/v1/stats/delivery"),
  putMail: (cfg: MailConfigReq) =>
    call<{ id: number }>("/notify", "/internal/v1/channels/mail", {
      method: "PUT",
      body: JSON.stringify(cfg),
    }),
  putSms: (cfg: SmsConfigReq) =>
    call<{ id: number }>("/notify", "/internal/v1/channels/sms", {
      method: "PUT",
      body: JSON.stringify(cfg),
    }),
  testMail: (to: string) =>
    call<{ sent: boolean }>("/notify", "/internal/v1/channels/mail/test", {
      method: "POST",
      body: JSON.stringify({ to }),
    }),
};

export const llmApi = {
  list: () => call<{ providers: LlmProvider[]; models: LlmModel[] }>("/ai", "/internal/v1/llm/providers"),
  createProvider: (p: { name: string; base_url: string; api_key: string; extra?: Record<string, unknown> }) =>
    call<{ id: number }>("/ai", "/internal/v1/llm/providers", { method: "POST", body: JSON.stringify(p) }),
  updateProvider: (id: number, patch: Record<string, unknown>) =>
    call<null>(`/ai`, `/internal/v1/llm/providers/${id}`, { method: "PUT", body: JSON.stringify(patch) }),
  createModel: (m: {
    provider_id: number;
    model_id: string;
    model_name: string;
    model_type?: string;
    weight?: number;
  }) => call<{ id: number }>("/ai", "/internal/v1/llm/models", { method: "POST", body: JSON.stringify(m) }),
  updateModel: (id: number, patch: Record<string, unknown>) =>
    call<null>(`/ai`, `/internal/v1/llm/models/${id}`, { method: "PUT", body: JSON.stringify(patch) }),
};

// biz-service 经 APISIX /api/v1（vite proxy 直连 23026 由本机 dev 配置；U1 内网经 APISIX）
export const bizApi = {
  grantPlan: (p: { userId: number; planType: string; expireTime?: number; operator?: string }) =>
    fetch("/biz/api/v1/admin/commerce/grant", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-Internal-Token": getToken() },
      body: JSON.stringify(p),
    }).then(parseBiz),
};

// ai-query few-shot 样本（B27 数据飞轮：UNKNOWN 回流标注 / 正样本管理）
export const fewshotApi = {
  list: (status?: string) =>
    call<{ samples: FewshotSample[] }>("/ai", `/internal/v1/fewshot${status ? `?status=${encodeURIComponent(status)}` : ""}`),
  stats: () => call<{ counts: Record<string, number> }>("/ai", "/internal/v1/fewshot/stats"),
  patch: (id: number, patch: { status?: string; condition_json?: string }) =>
    call<null>(`/ai`, `/internal/v1/fewshot/${id}`, { method: "PATCH", body: JSON.stringify(patch) }),
};

export interface FewshotSample {
  id: number;
  question: string;
  condition_json: string | null;
  source: string;
  status: string;
  hit_count: number;
  created_at: string;
}

async function parseBiz(resp: Response): Promise<unknown> {
  const body = await resp.json().catch(() => ({ code: -1, msg: `http ${resp.status}` }));
  if (body.code !== 0) throw new ApiError(body.code, body.msg ?? "unknown", resp.status);
  return body.data;
}

export interface ChannelRow {
  id: number;
  channel: "mail" | "sms";
  name: string;
  enabled: boolean;
  config: Record<string, unknown>;
}

export interface MailConfigReq {
  name?: string;
  host: string;
  port: number;
  tls_mode?: string;
  username: string;
  password: string;
  sender: string;
  enabled?: boolean;
}

export interface SmsConfigReq {
  name?: string;
  provider?: string;
  ak_id: string;
  ak_secret: string;
  sign: string;
  template: string;
  endpoint?: string;
  enabled?: boolean;
}

export interface LlmProvider {
  id: number;
  name: string;
  base_url: string;
  api_key: string;
  extra: string | null;
  enabled: boolean;
}

export interface LlmModel {
  id: number;
  provider_id: number;
  model_id: string;
  model_name: string;
  model_type: string;
  weight: number;
  max_tokens: number;
  enabled: boolean;
}

export interface DeliveryRuleStat {
  rule_id: number;
  market: string;
  symbol: string;
  pending: number;
  sent: number;
  acked: number;
  expired: number;
}
