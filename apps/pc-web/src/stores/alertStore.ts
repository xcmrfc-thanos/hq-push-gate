import { create } from "zustand";
import type { AlertPush } from "@hq/shared";

export interface AlertItem extends AlertPush {
  acked: boolean;
  receivedAt: number;
}

interface AlertState {
  items: AlertItem[];
  push: (list: AlertPush[]) => void;
  markAcked: (deliveryIds: string[]) => void;
}

export const useAlertStore = create<AlertState>((set) => ({
  items: [],
  push: (list) =>
    set((s) => ({
      items: [...list.map((a) => ({ ...a, acked: false, receivedAt: Date.now() })), ...s.items].slice(0, 500),
    })),
  markAcked: (deliveryIds) =>
    set((s) => ({
      items: s.items.map((a) =>
        deliveryIds.includes(a.deliveryId) ? { ...a, acked: true } : a,
      ),
    })),
}));
