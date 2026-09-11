import { ApiClient, QuoteSocket } from "@hq/api-client";
import { create } from "zustand";
import { useAuthStore } from "../stores/authStore";
import { useQuoteStore } from "../stores/quoteStore";
import { useAlertStore } from "../stores/alertStore";

export const apiClient = new ApiClient(""); // 相对根：/api 由 vite 代理（生产由 APISIX 路由）
apiClient.setOnUnauthorized(() => useAuthStore.getState().logout());

type SocketState = "connecting" | "open" | "closed";
interface SocketCtl {
  state: SocketState;
  setState: (s: SocketState) => void;
}

export const useSocketStore = create<SocketCtl>((set) => ({
  state: "closed",
  setState: (state) => set({ state }),
}));

let socket: QuoteSocket | null = null;
let started = false;

/** 获取进程级单例 socket（登录后创建）。 */
export function getSocket(): QuoteSocket {
  if (!socket) {
    socket = new QuoteSocket(wsUrl(), {
      onStateChange: (s) => {
        useSocketStore.getState().setState(s);
        if (s === "open") reconnectRecovery();
      },
      onQuotes: (qs) => useQuoteStore.getState().upsert(qs),
      onAlerts: (as) => {
        useAlertStore.getState().push(as);
        apiClient.ackAlert(as[0]?.deliveryId).catch(() => {});
      },
      onSys: (sys) => {
        if (sys.code === "drain") socket?.close();
      },
    });
  }
  return socket;
}

/**
 * 挂起/恢复 WS（登录态驱动）。docs/04 §7-3：重连成功先 REST 快照/补拉，再订阅。
 */
export function useSocket() {
  const hasToken = useAuthStore((s) => s.accessToken);
  if (hasToken && !started) {
    started = true;
    const s = getSocket();
    s.setToken(useAuthStore.getState().accessToken);
    // WS token：U0 直接复用 access token（biz-service 签发同密钥 JWT）
    s.open();
    reconnectRecovery();
  }
  if (!hasToken && started) {
    started = false;
    socket?.close();
    socket = null;
  }
}

/** 重连恢复：REST 快照 + 72h 窗口内告警补拉（deliveryId 去重）。 */
async function reconnectRecovery() {
  const quoteStore = useQuoteStore.getState();
  const symbols = Object.keys(quoteStore.quotes);
  if (symbols.length > 0) {
    const { market } = split(symbols[0]);
    apiClient.snapshot(market, symbols.map((s) => split(s).symbol)).catch(() => {});
    getSocket().subscribe(symbols);
  }
  try {
    const data = await apiClient.pullAlerts(0, 100);
    useAlertStore.getState().push(
      data.items.map((r) => ({
        deliveryId: r.delivery_id,
        eventId: r.event_id,
        ruleId: r.rule_id,
        symbol: `${r.market}:${r.symbol}`,
        title: r.title,
        detail: "",
        ts: Date.parse(r.trigger_at),
      })),
    );
  } catch {
    // 补拉失败由下一次重连/轮询兜底
  }
}

function split(key: string): { market: string; symbol: string } {
  const i = key.indexOf(":");
  return i < 0 ? { market: "A_SHARE", symbol: key } : { market: key.slice(0, i), symbol: key.slice(i + 1) };
}

function wsUrl(): string {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${location.host}/ws`;
}
