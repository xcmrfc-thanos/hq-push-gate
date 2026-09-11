import { create } from "zustand";
import type { AlertPush } from "@hq/shared";

export interface AlertItem extends AlertPush {
  receivedAt: number;
}

interface AlertState {
  items: AlertItem[];
  push: (list: AlertPush[]) => void;
}

export const useAlertStore = create<AlertState>((set) => ({
  items: [],
  push: (list) =>
    set((s) => ({
      items: [...list.map((a) => ({ ...a, receivedAt: Date.now() })), ...s.items].slice(0, 200),
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
