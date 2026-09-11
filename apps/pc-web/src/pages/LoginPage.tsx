import { Button, Card, Form, Input, Tabs, Typography, message } from "antd";
import { useNavigate } from "react-router-dom";
import { apiClient } from "../services/socket";
import { useAuthStore } from "../stores/authStore";

export function LoginPage() {
  const nav = useNavigate();
  const setSession = useAuthStore((s) => s.setSession);

  const onAuth = async (mode: "login" | "register", values: { username: string; password: string }) => {
    try {
      const data = mode === "login"
        ? await apiClient.login(values.username, values.password)
        : await apiClient.register(values.username, values.password);
      setSession(data.user_id, data.access_token, data.refresh_token);
      nav("/market");
    } catch (e) {
      void message.error((e as Error).message);
    }
  };

  return (
    <div
      style={{
        minHeight: "100vh",
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        background: "linear-gradient(135deg, #0b1f4d 0%, #1d4ed8 48%, #6d28d9 100%)",
      }}
    >
      <Typography.Title level={2} style={{ color: "#fff", marginBottom: 4 }}>
        hq-push-gate
      </Typography.Title>
      <Typography.Paragraph style={{ color: "rgba(255,255,255,0.75)", marginBottom: 32 }}>
        多市场行情 · 条件预警 · 实时推送
      </Typography.Paragraph>
      <Card
        style={{
          width: 400,
          borderRadius: 16,
          boxShadow: "0 18px 50px rgba(0,0,0,0.35)",
        }}
        styles={{ body: { padding: 28 } }}
      >
        <Tabs
          centered
          items={[
            { key: "login", label: "登录", children: <AuthForm onFinish={(v) => onAuth("login", v)} /> },
            { key: "reg", label: "注册", children: <AuthForm onFinish={(v) => onAuth("register", v)} /> },
          ]}
        />
      </Card>
      <Typography.Text style={{ color: "rgba(255,255,255,0.55)", marginTop: 24, fontSize: 12 }}>
        U0 开发环境 · 服务端保留窗口 72h 内告警可补拉
      </Typography.Text>
    </div>
  );
}

function AuthForm({ onFinish }: { onFinish: (v: { username: string; password: string }) => void }) {
  return (
    <Form layout="vertical" onFinish={onFinish} requiredMark={false}>
      <Form.Item name="username" label="用户名" rules={[{ required: true, message: "请输入用户名" }]}>
        <Input placeholder="username" size="large" />
      </Form.Item>
      <Form.Item name="password" label="密码" rules={[{ required: true, min: 6, message: "至少 6 位" }]}>
        <Input.Password placeholder="至少 6 位" size="large" />
      </Form.Item>
      <Button type="primary" htmlType="submit" block size="large">
        提交
      </Button>
    </Form>
  );
}
