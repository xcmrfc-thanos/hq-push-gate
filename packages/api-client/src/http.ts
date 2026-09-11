import type { ApiEnvelope, AlertRecord, AlertRule, KlineBar, QuoteSnapshot } from "@hq/shared";

/** REST 客户端：统一信封解析、Bearer 注入、401 换发（docs/04 §6）。 */
export class ApiClient {
  private baseUrl: string;
  private token: string | null = null;
  private refreshToken: string | null = null;
  private onUnauthorized?: () => void;

  constructor(baseUrl: string) {
    this.baseUrl = baseUrl.replace(/\/$/, "");
  }

  setTokens(access: string | null, refresh?: string | null) {
    this.token = access;
    if (refresh !== undefined) this.refreshToken = refresh;
  }

  getAccessToken(): string | null {
    return this.token;
  }

  setOnUnauthorized(fn: () => void) {
    this.onUnauthorized = fn;
  }

  private async request<T>(path: string, init?: RequestInit): Promise<T> {
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
      ...(init?.headers as Record<string, string> | undefined),
    };
    if (this.token) headers.Authorization = `Bearer ${this.token}`;
    const resp = await fetch(this.baseUrl + path, { ...init, headers });
    const body = (await resp.json()) as ApiEnvelope<T>;
    if (body.code === 40101) {
      // access 过期尝试换发一次
      const refreshed = this.refreshToken ? await this.doRefresh() : false;
      if (refreshed) {
        return this.request<T>(path, init);
      }
      this.onUnauthorized?.();
      throw new Error("unauthorized");
    }
    if (body.code !== 0) {
      throw new Error(body.msg || `api error code=${body.code}`);
    }
    return body.data as T;
  }

  private async doRefresh(): Promise<boolean> {
    try {
      const data = await this.request<{ access_token: string }>("/api/v1/auth/refresh", {
        method: "POST",
        body: JSON.stringify({ refresh_token: this.refreshToken }),
      });
      this.token = data.access_token;
      return true;
    } catch {
      return false;
    }
  }

  // ---- auth ----
  register(username: string, password: string) {
    return this.request<{ user_id: number; access_token: string; refresh_token: string }>(
      "/api/v1/auth/register",
      { method: "POST", body: JSON.stringify({ username, password }) },
    );
  }

  login(username: string, password: string) {
    return this.request<{ user_id: number; access_token: string; refresh_token: string }>(
      "/api/v1/auth/login",
      { method: "POST", body: JSON.stringify({ username, password }) },
    );
  }

  /** 演示辅助：为 WS 生成 access token（联调用） */
  wsToken() {
    return this.request<{ ws_token: string }>("/api/v1/auth/ws-token");
  }

  // ---- quote ----
  snapshot(market: string, symbols: string[]) {
    const q = new URLSearchParams({ market, symbols: symbols.join(",") });
    return this.request<QuoteSnapshot[]>(`/api/v1/quote/snapshot?${q}`);
  }

  /** 历史K线（docs/04 §6）：period 1/5 基期，15/30/60/1440 服务端由 1m 滚动。 */
  kline(market: string, symbol: string, period: number, limit = 500) {
    const q = new URLSearchParams({
      market, symbol, period: String(period), limit: String(limit),
    });
    return this.request<KlineBar[]>(`/api/v1/quote/kline?${q}`);
  }

  // ---- rules ----
  listRules() {
    return this.request<AlertRule[]>("/api/v1/rules");
  }

  createRule(rule: AlertRule) {
    return this.request<AlertRule>("/api/v1/rules", { method: "POST", body: JSON.stringify(rule) });
  }

  updateRule(id: number, rule: AlertRule) {
    return this.request<AlertRule>(`/api/v1/rules/${id}`, {
      method: "PUT",
      body: JSON.stringify(rule),
    });
  }

  updateRuleStatus(id: number, status: number) {
    return this.request<AlertRule>(`/api/v1/rules/${id}/status`, {
      method: "PATCH",
      body: JSON.stringify({ status }),
    });
  }

  deleteRule(id: number) {
    return this.request<void>(`/api/v1/rules/${id}`, { method: "DELETE" });
  }

  // ---- alerts 补拉（docs/04 §6：GET /alerts?cursor=&limit=） ----
  pullAlerts(cursor = 0, limit = 50) {
    return this.request<{ items: AlertRecord[]; next_cursor: number }>(
      `/api/v1/alerts?cursor=${cursor}&limit=${limit}`,
    );
  }

  ackAlert(deliveryId: string) {
    return this.request<{ updated: boolean }>("/api/v1/alerts/ack", {
      method: "POST",
      body: JSON.stringify({ delivery_id: deliveryId }),
    });
  }
}
