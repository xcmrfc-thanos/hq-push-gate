import { Button, Card, Form, Input, Tabs, Toast } from "antd-mobile";
import { useNavigate } from "react-router-dom";
import { apiClient } from "../services/socket";
import { useAuthStore } from "../stores/authStore";

export function LoginPage() {
  const nav = useNavigate();
  const setSession = useAuthStore((s) => s.setSession);

  const onAuth = async (mode: "login" | "register", v: { username: string; password: string }) => {
    try {
      const data = mode === "login"
        ? await apiClient.login(v.username, v.password)
        : await apiClient.register(v.username, v.password);
      setSession(data.user_id, data.access_token, data.refresh_token);
      nav("/quotes");
    } catch (e) {
      Toast.show({ content: (e as Error).message });
    }
  };

  return (
    <div
      style={{
        minHeight: "100vh",
        padding: 24,
        paddingTop: 72,
        boxSizing: "border-box",
        background: "linear-gradient(160deg, #0b1f4d 0%, #1d4ed8 55%, #6d28d9 100%)",
      }}
    >
      <h2 style={{ textAlign: "center", color: "#fff", letterSpacing: 1 }}>hq-push-gate</h2>
      <p style={{ textAlign: "center", color: "rgba(255,255,255,0.75)", marginTop: 4, marginBottom: 24 }}>
        多市场行情 · 条件预警 · 实时推送
      </p>
      <Tabs>
        <Tabs.Tab title="登录" key="login">
          <Card>
            <AuthForm submitText="登录" onFinish={(v) => onAuth("login", v)} />
          </Card>
        </Tabs.Tab>
        <Tabs.Tab title="注册" key="reg">
          <Card>
            <AuthForm submitText="注册" onFinish={(v) => onAuth("register", v)} />
          </Card>
        </Tabs.Tab>
      </Tabs>
    </div>
  );
}

function AuthForm({
  submitText,
  onFinish,
}: {
  submitText: string;
  onFinish: (v: { username: string; password: string }) => void;
}) {
  return (
    <Form
      layout="vertical"
      onFinish={(v) => onFinish(v as { username: string; password: string })}
      footer={<Button block type="submit" color="primary" size="large">{submitText}</Button>}
    >
      <Form.Item name="username" label="用户名" rules={[{ required: true }]}>
        <Input placeholder="请输入用户名" />
      </Form.Item>
      <Form.Item name="password" label="密码" rules={[{ required: true, min: 6 }]}>
        <Input type="password" placeholder="至少 6 位" />
      </Form.Item>
    </Form>
  );
}
