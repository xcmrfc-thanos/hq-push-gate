import { ConfigProvider, Layout, Menu, Badge, Typography, Switch } from "antd";
import zhCN from "antd/locale/zh_CN";
import { useEffect, useMemo, useState } from "react";
import { Navigate, Outlet, Route, Routes, useLocation, useNavigate } from "react-router-dom";
import { BrowserRouter, Link } from "react-router-dom";
import { useAuthStore } from "../stores/authStore";
import { useAlertStore } from "../stores/alertStore";
import { useUiStore } from "../stores/uiStore";
import { useSocket, useSocketStore } from "../services/socket";
import { themeConfig } from "./theme";
import { LoginPage } from "../pages/LoginPage";
import { MarketPage } from "../pages/MarketPage";
import { AlertsPage } from "../pages/AlertsPage";
import { RulesPage } from "../pages/RulesPage";
import { ChartPage } from "../pages/ChartPage";

const { Header, Sider, Content } = Layout;

export function App() {
  const mode = useUiStore((s) => s.mode);
  return (
    <ConfigProvider locale={zhCN} theme={themeConfig(mode)}>
      <BrowserRouter>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route element={<RequireAuth />}>
            <Route path="/" element={<Shell />}>
              <Route index element={<Navigate to="/market" replace />} />
              <Route path="/market" element={<MarketPage />} />
              <Route path="/chart" element={<ChartPage />} />
              <Route path="/alerts" element={<AlertsPage />} />
              <Route path="/rules" element={<RulesPage />} />
            </Route>
          </Route>
        </Routes>
      </BrowserRouter>
    </ConfigProvider>
  );
}

function RequireAuth() {
  const token = useAuthStore((s) => s.accessToken);
  useSocket(); // 登录后挂起 WS 生命周期
  return token ? <Outlet /> : <Navigate to="/login" replace />;
}

function Shell() {
  const nav = useNavigate();
  const loc = useLocation();
  const logout = useAuthStore((s) => s.logout);
  const userId = useAuthStore((s) => s.userId);
  const mode = useUiStore((s) => s.mode);
  const toggle = useUiStore((s) => s.toggle);
  const unread = useAlertStore((s) => s.items.filter((a) => !a.acked).length);
  const [collapsed, setCollapsed] = useState(false);

  const items = useMemo(
    () => [
      { key: "/market", label: "行情" },
      { key: "/chart", label: "K线" },
      { key: "/alerts", label: <Badge count={unread} offset={[8, 0]}>告警</Badge> },
      { key: "/rules", label: "规则" },
    ],
    [unread],
  );

  return (
    <Layout style={{ minHeight: "100vh" }}>
      <Sider collapsible collapsed={collapsed} onCollapse={setCollapsed} theme="dark">
        <div style={{ color: "#fff", padding: 16, fontWeight: 600, letterSpacing: 1 }}>
          {!collapsed && <Link to="/" style={{ color: "#fff" }}>智析平台</Link>}
        </div>
        <Menu
          theme="dark"
          mode="inline"
          selectedKeys={[loc.pathname]}
          items={items}
          onClick={(e) => nav(e.key)}
        />
      </Sider>
      <Layout>
        <Header
          style={{
            display: "flex",
            justifyContent: "flex-end",
            alignItems: "center",
            gap: 20,
            borderBottom: "1px solid rgba(128,128,128,0.15)",
          }}
        >
          <ConnectionState />
          <Typography.Text type="secondary">用户 #{userId ?? "-"}</Typography.Text>
          <Switch
            checked={mode === "dark"}
            checkedChildren="🌙 暗色"
            unCheckedChildren="☀️ 亮色"
            onChange={toggle}
          />
          <Typography.Link onClick={logout}>退出登录</Typography.Link>
        </Header>
        <Content style={{ margin: 16 }}>
          <div key={loc.pathname} className="fade-in">
            <Outlet />
          </div>
        </Content>
      </Layout>
    </Layout>
  );
}

function ConnectionState() {
  const state = useSocketStore((s) => s.state);
  const label = state === "open" ? "已连接" : state === "connecting" ? "连接中" : "已断开";
  const color = state === "open" ? "#52c41a" : state === "connecting" ? "#faad14" : "#ff4d4f";
  return (
    <span style={{ display: "inline-flex", alignItems: "center", gap: 8 }} data-ws-state={state}>
      {state === "open"
        ? <i className="pulse" />
        : <i style={{ display: "inline-block", width: 8, height: 8, borderRadius: "50%", background: color }} />}
      <Typography.Text type="secondary">{label}</Typography.Text>
    </span>
  );
}
