import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 23031,
    proxy: {
      // 开发期代理到 biz-service 与 ws-gateway（生产由 APISIX 统一路由，端口规划见 deploy/docker-compose.yml 头注释）
      "/api": { target: "http://localhost:23026", changeOrigin: true },
      "/ws": { target: "ws://localhost:23024", ws: true },
    },
  },
});
