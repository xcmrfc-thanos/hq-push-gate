# apps/ 前端（pc-web、h5）

React 19 + Node 24 + Vite 双端（npm workspaces，共享 `@hq/api-client` 与 `@hq/shared`）。

| 应用 | 技术栈 | 页面 |
|---|---|---|
| `pc-web/` | React 19 + AntD 6 + klinecharts 10 + zustand | 登录/注册、实时行情、K线（客户端 1m 聚合演示）、告警中心（ACK/补拉去重）、规则管理 |
| `h5/` | React 19 + AntD Mobile 5 + zustand | 登录、行情列表、告警中心 |

## 契约实现（docs/04 §7，冻结项）

`@hq/api-client` 的 `QuoteSocket` 实现全部客户端约定：

- 30s 心跳 ping；
- 断线 1s 起指数退避（上限 60s）+ 随机抖动；
- 重连成功先 REST 快照/补拉（`pullAlerts` cursor），再恢复订阅；
- 告警按 `deliveryId` 幂等去重并自动 ACK；
- 页面不可见自动 unsub 行情、仅保留 alert 通道，恢复可见后重新订阅；
- `sys/drain` 通知触发立即重连。

## 命令

```bash
npm install        # 根目录 workspace 安装
npm run build      # 双端 tsc --noEmit + vite build
npm run dev --workspace apps/pc-web   # 23031
npm run dev --workspace apps/h5       # 23032
```

开发期 `/api` 代理到 biz-service(8086)，`/ws` 需单独代理（或在 vite.config.ts 增加 `/ws` proxy 指向 ws-gateway:8084）。生产由 APISIX 统一路由（docs/07 §2）。

目录规范见 docs/08 §4.3。
