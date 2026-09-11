import { WS_LIMITS } from "@hq/shared";
import type { AlertPush, QuotePush } from "@hq/shared";
import type { ServerMsg, SysData } from "./wsTypes";

export interface QuoteSocketCallbacks {
  onQuotes?: (quotes: QuotePush[]) => void;
  onAlerts?: (alerts: AlertPush[]) => void;
  onSys?: (sys: SysData) => void;
  onStateChange?: (state: "connecting" | "open" | "closed") => void;
}

/**
 * 行情/告警 WebSocket 客户端，实现冻结协议（docs/04 §7）：
 * 1. 30s 心跳 ping；
 * 2. 断线 1s 起指数退避（1/2/4/8...上限 60s）+ 随机抖动；
 * 3. 重连成功先由调用方 REST 补快照/补拉，再恢复订阅；
 * 4. 告警按 deliveryId 幂等去重并自动 ACK（REST 补拉重发同 ID 不会重复回调）；
 * 5. 页面不可见自动 unsub 行情、仅保留 alert 通道；恢复可见后重新订阅。
 */
export class QuoteSocket {
  private ws: WebSocket | null = null;
  private url: string;
  private token: string | null = null;
  private symbols = new Set<string>();
  private cb: QuoteSocketCallbacks;
  private seenDeliveries = new Set<string>(); // deliveryId 去重（重连补拉可能重发）
  private heartbeat?: ReturnType<typeof setInterval>;
  private retryTimer?: ReturnType<typeof setTimeout>;
  private attempt = 0;
  private closedByUser = false;

  constructor(url: string, cb: QuoteSocketCallbacks) {
    this.url = url.replace(/\/$/, "");
    this.cb = cb;
  }

  setToken(token: string | null) {
    this.token = token;
  }

  /** 当前订阅集。 */
  subscribed(): string[] {
    return [...this.symbols];
  }

  open() {
    this.closedByUser = false;
    this.connect();
    if (typeof document !== "undefined") {
      document.addEventListener("visibilitychange", this.onVisibility);
    }
  }

  close() {
    this.closedByUser = true;
    if (typeof document !== "undefined") {
      document.removeEventListener("visibilitychange", this.onVisibility);
    }
    this.clearTimers();
    this.ws?.close();
    this.ws = null;
    this.cb.onStateChange?.("closed");
  }

  /** 订阅标的（合并去重，受冻结上限保护）。 */
  subscribe(symbols: string[]) {
    const added = symbols.filter((s) => !this.symbols.has(s));
    for (const s of added) {
      if (this.symbols.size >= WS_LIMITS.MAX_SUBS_PER_CONN) break;
      this.symbols.add(s);
    }
    if (added.length > 0) {
      this.send({ type: "sub", symbols: [...this.symbols] }); // 发全量，服务端幂等合并
    }
  }

  unsubscribe(symbols: string[]) {
    for (const s of symbols) this.symbols.delete(s);
    if (symbols.length > 0) this.send({ type: "unsub", symbols });
  }

  private connect() {
    if (this.closedByUser) return;
    const u = new URL(this.url);
    if (this.token) u.searchParams.set("token", this.token);
    this.cb.onStateChange?.("connecting");
    const ws = new WebSocket(u.toString());
    this.ws = ws;

    ws.onopen = () => {
      this.attempt = 0;
      this.cb.onStateChange?.("open");
      // 重连恢复：调用方 onStateChange(open) 时先 REST 快照/补拉，随后恢复订阅
      if (this.symbols.size > 0) this.send({ type: "sub", symbols: [...this.symbols] });
      this.heartbeat = setInterval(() => this.send({ type: "ping" }), WS_LIMITS.HEARTBEAT_SEC * 1000);
    };

    ws.onmessage = (ev) => {
      let msg: ServerMsg;
      try {
        msg = JSON.parse(ev.data as string) as ServerMsg;
      } catch {
        return;
      }
      switch (msg.type) {
        case "quote": {
          const quotes = msg.data as QuotePush[];
          if (Array.isArray(quotes)) this.cb.onQuotes?.(quotes);
          break;
        }
        case "alert": {
          const a = msg.data as AlertPush;
          if (!a?.deliveryId) break;
          if (this.seenDeliveries.has(a.deliveryId)) break; // 幂等去重（docs/04 §7-2）
          this.seenDeliveries.add(a.deliveryId);
          if (this.seenDeliveries.size > 5000) {
            // 有界集合：丢弃最早一半
            const arr = [...this.seenDeliveries];
            for (let i = 0; i < 2500; i++) this.seenDeliveries.delete(arr[i]);
          }
          this.send({ type: "ack", deliveryId: a.deliveryId }); // 收到即 ACK
          this.cb.onAlerts?.([a]);
          break;
        }
        case "sys": {
          const sys = (msg.data ?? {}) as SysData;
          if (sys.code === "drain") {
            // 实例缩容：立即重连到其它实例
            this.ws?.close();
          } else {
            this.cb.onSys?.(sys);
          }
          break;
        }
      }
    };

    ws.onclose = () => {
      this.clearTimers();
      if (this.closedByUser) return;
      this.scheduleReconnect();
    };
    ws.onerror = () => ws.close();
  }

  private scheduleReconnect() {
    this.cb.onStateChange?.("closed");
    const base = Math.min(
      WS_LIMITS.RECONNECT_MIN_MS * 2 ** this.attempt,
      WS_LIMITS.RECONNECT_MAX_MS,
    );
    const delay = base / 2 + Math.random() * (base / 2); // 随机抖动
    this.attempt++;
    this.retryTimer = setTimeout(() => this.connect(), delay);
  }

  private onVisibility = () => {
    if (this.closedByUser || !this.ws) return;
    if (document.visibilityState === "hidden" && this.symbols.size > 0) {
      // docs/04 §7-4：页面不可见仅保留 alert 通道
      const all = [...this.symbols];
      this.send({ type: "unsub", symbols: all });
      this.pendingResubscribe = all;
    } else if (document.visibilityState === "visible" && this.pendingResubscribe.length > 0) {
      this.send({ type: "sub", symbols: this.pendingResubscribe });
      this.pendingResubscribe = [];
    }
  };

  private pendingResubscribe: string[] = [];

  private send(msg: unknown) {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(msg));
    }
  }

  private clearTimers() {
    if (this.heartbeat) clearInterval(this.heartbeat);
    if (this.retryTimer) clearTimeout(this.retryTimer);
    this.heartbeat = undefined;
    this.retryTimer = undefined;
  }
}
