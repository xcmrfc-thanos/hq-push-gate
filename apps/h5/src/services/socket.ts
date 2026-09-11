import { ApiClient, QuoteSocket } from "@hq/api-client";
import { useAuthStore } from "../stores/authStore";
import { useAlertStore, useQuoteStore } from "../stores/stores";

export const apiClient = new ApiClient("");
apiClient.setOnUnauthorized(() => useAuthStore.getState().logout());

let socket: QuoteSocket | null = null;
let started = false;

/** 进程级单例（登录态由 useSocket 驱动）。 */
export function getSocket(): QuoteSocket {
  if (!socket) {
    socket = new QuoteSocket(wsUrl(), {
      onStateChange: (s) => {
        if (s === "open") void recover();
      },
      onQuotes: (qs) => useQuoteStore.getState().upsert(qs),
      onAlerts: (as) => {
        useAlertStore.getState().push(as);
        apiClient.ackAlert(as[0]?.deliveryId).catch(() => {});
      },
    });
  }
  return socket;
}

export function useSocket() {
  const hasToken = useAuthStore((s) => s.accessToken);
  if (hasToken && !started) {
    started = true;
    const s = getSocket();
    s.setToken(useAuthStore.getState().accessToken);
    s.open();
    void recover();
  }
  if (!hasToken && started) {
    started = false;
    socket?.close();
    socket = null;
  }
}

/** 重连恢复：REST 快照 + 告警补拉（docs/04 §7-3）。 */
async function recover() {
  try {
    const rows = await apiClient.snapshot("A_SHARE", Array.from({ length: 10 }, (_, i) => `60000${i}`));
    useQuoteStore.getState().upsert(
      rows.map((r, i) => ({
        symbol: `A_SHARE:60000${i}`,
        last: Number(r.last), pct: Number(r.pct), vol: Number(r.vol), ts: Number(r.ts),
      })),
    );
    socket?.subscribe(rows.map((_, i) => `A_SHARE:60000${i}`));
  } catch { /* 重连后重试 */ }
  try {
    const data = await apiClient.pullAlerts(0, 50);
    useAlertStore.getState().push(
      data.items.map((r) => ({
        deliveryId: r.delivery_id, eventId: r.event_id, ruleId: r.rule_id,
        symbol: `${r.market}:${r.symbol}`, title: r.title, detail: "",
        ts: Date.parse(r.trigger_at),
      })),
    );
  } catch { /* 忽略，由下一次补拉兜底 */ }
}

function wsUrl(): string {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${location.host}/ws`;
}
