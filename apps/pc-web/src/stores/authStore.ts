import { create } from "zustand";
import { persist } from "zustand/middleware";

interface AuthState {
  userId: number | null;
  accessToken: string | null;
  refreshToken: string | null;
  setSession: (userId: number, access: string, refresh: string) => void;
  setAccessToken: (t: string) => void;
  logout: () => void;
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      userId: null,
      accessToken: null,
      refreshToken: null,
      setSession: (userId, accessToken, refreshToken) => set({ userId, accessToken, refreshToken }),
      setAccessToken: (accessToken) => set({ accessToken }),
      logout: () => set({ userId: null, accessToken: null, refreshToken: null }),
    }),
    { name: "hq-auth" },
  ),
);
