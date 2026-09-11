import { create } from "zustand";
import { persist } from "zustand/middleware";

export type ThemeMode = "light" | "dark";

interface UiState {
  mode: ThemeMode;
  toggle: () => void;
}

/** 外观偏好（持久化到 localStorage）。 */
export const useUiStore = create<UiState>()(
  persist(
    (set) => ({
      mode: "light",
      toggle: () => set((s) => ({ mode: s.mode === "light" ? "dark" : "light" })),
    }),
    { name: "hq-ui" },
  ),
);
