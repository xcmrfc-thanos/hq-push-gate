import { NavBar, TabBar } from "antd-mobile";
import { Navigate, Route, Routes, useLocation, useNavigate, Outlet } from "react-router-dom";
import { useAuthStore } from "../stores/authStore";
import { useSocket } from "../services/socket";
import { LoginPage } from "../pages/LoginPage";
import { QuotesPage } from "../pages/QuotesPage";
import { AIScreenPage } from "../pages/AIScreenPage";
import { AlertsPage } from "../pages/AlertsPage";

export function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route element={<RequireAuth />}>
        <Route path="/" element={<Shell />}>
          <Route index element={<Navigate to="/quotes" replace />} />
          <Route path="/quotes" element={<QuotesPage />} />
          <Route path="/ai" element={<AIScreenPage />} />
          <Route path="/alerts" element={<AlertsPage />} />
        </Route>
      </Route>
    </Routes>
  );
}

function RequireAuth() {
  const token = useAuthStore((s) => s.accessToken);
  useSocket();
  return token ? <Outlet /> : <Navigate to="/login" replace />;
}

function Shell() {
  const loc = useLocation();
  const nav = useNavigate();
  return (
    <div style={{ height: "100vh", display: "flex", flexDirection: "column" }}>
      <NavBar back={null} style={{ background: "#fff", borderBottom: "1px solid #eee" }}>
        智析平台
      </NavBar>
      <div style={{ flex: 1, overflow: "auto" }}>
        <Outlet />
      </div>
      <TabBar activeKey={loc.pathname} onChange={(k) => nav(k)}>
        <TabBar.Item key="/quotes" title="行情" />
        <TabBar.Item key="/ai" title="选股" />
        <TabBar.Item key="/alerts" title="告警" badge={undefined} />
      </TabBar>
    </div>
  );
}
