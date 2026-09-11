import { create } from "zustand";
import type { QuotePush } from "@hq/shared";

interface QuoteState {
  /** key: "A_SHARE:600000" */
  quotes: Record<string, QuotePush>;
  upsert: (list: QuotePush[]) => void;
  remove: (symbols: string[]) => void;
}

export const useQuoteStore = create<QuoteState>((set) => ({
  quotes: {},
  upsert: (list) =>
    set((s) => {
      const next = { ...s.quotes };
      for (const q of list) next[q.symbol] = q; // 丢旧保新：只保留最新
      return { quotes: next };
    }),
  remove: (symbols) =>
    set((s) => {
      const next = { ...s.quotes };
      for (const k of symbols) delete next[k];
      return { quotes: next };
    }),
}));
