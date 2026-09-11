import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 23032,
    proxy: {
      "/api": { target: "http://localhost:23026", changeOrigin: true },
      "/ws": { target: "ws://localhost:23024", ws: true },
    },
  },
});
