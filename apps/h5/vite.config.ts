import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 23032,
    proxy: {
      "/api/v1/ai": { target: "http://localhost:23041", changeOrigin: true }, // B26：ai-query 独立端口，须排在 /api 之前
      "/api": { target: "http://localhost:23026", changeOrigin: true },
      "/ws": { target: "ws://localhost:23024", ws: true },
    },
  },
});
