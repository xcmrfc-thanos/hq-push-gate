import { create } from "zustand";
import type { AlertPush } from "@hq/shared";

export interface AlertItem extends AlertPush {
  receivedAt: number;
  acked?: boolean;
}

interface AlertState {
  items: AlertItem[];
  push: (list: AlertPush[]) => void;
  /** 客户端 ACK：本地置灰（服务端状态由 ackAlert 转发兜底） */
  markAcked: (deliveryIds: string[]) => void;
}

export const useAlertStore = create<AlertState>((set) => ({
  items: [],
  push: (list) =>
    set((s) => ({
      items: [...list.map((a) => ({ ...a, receivedAt: Date.now() })), ...s.items].slice(0, 200),
    })),
  markAcked: (deliveryIds) =>
    set((s) => ({
      items: s.items.map((a) =>
        deliveryIds.includes(a.deliveryId) ? { ...a, acked: true } : a,
      ),
    })),
}));

interface QuoteState {
  quotes: Record<string, { symbol: string; last: number; pct: number; vol: number; ts: number }>;
  upsert: (list: { symbol: string; last: number; pct: number; vol: number; ts: number }[]) => void;
}

export const useQuoteStore = create<QuoteState>((set) => ({
  quotes: {},
  upsert: (list) =>
    set((s) => {
      const next = { ...s.quotes };
      for (const q of list) next[q.symbol] = q;
      return { quotes: next };
    }),
}));
