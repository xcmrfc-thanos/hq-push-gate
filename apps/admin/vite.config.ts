import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// admin 仅内网/本机运行：dev 代理直连 notify(23023)/ai-query(23041) 规避 CORS，
// 不经 APISIX（/internal/* 不出公网口径，docs/11 §1.1）。
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5175,
    proxy: {
      "/notify": { target: "http://127.0.0.1:23023", changeOrigin: true, rewrite: (p) => p.replace(/^\/notify/, "") },
      "/ai": { target: "http://127.0.0.1:23041", changeOrigin: true, rewrite: (p) => p.replace(/^\/ai/, "") },
      "/biz": { target: "http://127.0.0.1:23026", changeOrigin: true, rewrite: (p) => p.replace(/^\/biz/, "") },
    },
  },
});
