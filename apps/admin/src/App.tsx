import { useCallback, useEffect, useState } from "react";
import {
  Alert, Button, Card, Form, Input, InputNumber, Layout, Select, Space, Table, Tabs, Tag, Typography, message,
} from "antd";
import { ApiError, bizApi, getToken, llmApi, notifyApi, setToken, type ChannelRow, type LlmModel, type LlmProvider } from "./api";

const { Header, Content } = Layout;

export function App() {
  const [hasToken, setHasToken] = useState(getToken() !== "");
  return (
    <Layout style={{ minHeight: "100vh" }}>
      <Header style={{ color: "#fff", fontSize: 16, display: "flex", alignItems: "center", gap: 16 }}>
        hq-push-gate 管理台（内网）
        <TokenBox onChange={setHasToken} />
      </Header>
      <Content style={{ padding: 24 }}>
        {hasToken ? <ChannelTabs /> : <Alert type="warning" showIcon title="请先在右上角输入 X-Internal-Token" />}
      </Content>
    </Layout>
  );
}

function TokenBox({ onChange }: { onChange: (ok: boolean) => void }) {
  const save = () => {
    const v = (document.getElementById("tk") as HTMLInputElement)?.value.trim();
    if (v) {
      setToken(v);
      onChange(true);
      message.success("token 已保存（localStorage）");
    }
  };
  return (
    <Space.Compact style={{ marginLeft: "auto" }}>
      <Input id="tk" placeholder="X-Internal-Token" style={{ width: 240 }} defaultValue={getToken()} />
      <Button type="primary" onClick={save}>保存</Button>
    </Space.Compact>
  );
}

function ChannelTabs() {
  return (
    <Tabs
      items={[
        { key: "mail", label: "邮件渠道", children: <MailTab /> },
        { key: "sms", label: "短信渠道", children: <SmsTab /> },
        { key: "llm", label: "LLM 渠道", children: <LlmTab /> },
        { key: "users", label: "用户套餐", children: <CommerceTab /> },
      ]}
    />
  );
}

function CommerceTab() {
  const [form] = Form.useForm();
  const [loading, setLoading] = useState(false);
  const submit = async (v: Record<string, unknown>) => {
    setLoading(true);
    try {
      await bizApi.grantPlan({
        userId: Number(v.userId),
        planType: String(v.planType),
        expireTime: v.expireTime ? Number(v.expireTime) : undefined,
        operator: "admin-ui",
      });
      message.success("套餐已发放（admin_audit_log 留痕）");
      form.resetFields();
    } catch (e) {
      message.error(e instanceof ApiError ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  };
  return (
    <Card title="套餐发放（/api/v1/admin/commerce/grant，X-Internal-Token + 审计）">
      <Form form={form} layout="inline" onFinish={submit} initialValues={{ planType: "vip1" }}>
        <Form.Item name="userId" rules={[{ required: true }]}><InputNumber placeholder="用户 ID" min={1} /></Form.Item>
        <Form.Item name="planType">
          <Select style={{ width: 130 }}
            options={[{ value: "free" }, { value: "vip1" }, { value: "vip2" }, { value: "vip3" }]} />
        </Form.Item>
        <Form.Item name="expireTime">
          <InputNumber placeholder="过期时间戳(ms，可空)" style={{ width: 220 }} />
        </Form.Item>
        <Form.Item><Button type="primary" htmlType="submit" loading={loading}>发放</Button></Form.Item>
      </Form>
      <Typography.Text type="secondary" style={{ fontSize: 12 }}>
        档位（docs/10 §3.1）：free 5/20 无短信 · vip1 10/50 · vip2 20/100 · vip3 50/300（短信）。用户自助绑定：/api/v1/me/bindings/email、/bindings/phone、/bindings/webhooks（JWT）。
      </Typography.Text>
    </Card>
  );
}

function useChannels() {
  const [rows, setRows] = useState<ChannelRow[]>([]);
  const reload = useCallback(() => {
    notifyApi.listChannels().then((d) => setRows(d.items)).catch((e) => message.error(String(e)));
  }, []);
  useEffect(reload, [reload]);
  return { rows, reload };
}

function MailTab() {
  const { rows, reload } = useChannels();
  const [form] = Form.useForm();
  const [testing, setTesting] = useState(false);
  const submit = async (v: Record<string, unknown>) => {
    try {
      await notifyApi.putMail({ ...v, port: Number(v.port), enabled: true } as never);
      message.success("已保存并热生效（30s 内回源）");
      reload();
    } catch (e) {
      message.error(String(e));
    }
  };
  const test = async () => {
    const to = form.getFieldValue("test_to") as string | undefined;
    if (!to) return message.warning("先填测试收件邮箱");
    setTesting(true);
    try {
      await notifyApi.testMail(to);
      message.success("测试邮件已发送");
    } catch (e) {
      message.error(e instanceof ApiError ? e.message : String(e));
    } finally {
      setTesting(false);
    }
  };
  return (
    <Space direction="vertical" size="large" style={{ width: "100%" }}>
      <ChannelTable rows={rows.filter((r) => r.channel === "mail")} />
      <Card title="配置 SMTP（QQ/163：host=smtp.qq.com|smtp.163.com，port=465，password=授权码；企业：smtpdm.aliyun.com 等）">
        <Form form={form} layout="inline" onFinish={submit} initialValues={{ port: 465, tls_mode: "ssl", name: "default" }}>
          <Form.Item name="name" rules={[{ required: true }]}><Input placeholder="渠道名 default" /></Form.Item>
          <Form.Item name="host" rules={[{ required: true }]}><Input placeholder="smtp.qq.com" style={{ width: 180 }} /></Form.Item>
          <Form.Item name="port" rules={[{ required: true }]}><InputNumber placeholder="465" /></Form.Item>
          <Form.Item name="tls_mode"><Select style={{ width: 110 }} options={[{ value: "ssl" }, { value: "starttls" }, { value: "plain" }]} /></Form.Item>
          <Form.Item name="username" rules={[{ required: true }]}><Input placeholder="发信账号" style={{ width: 200 }} /></Form.Item>
          <Form.Item name="password" rules={[{ required: true }]}><Input.Password placeholder="密码/授权码" style={{ width: 180 } } /></Form.Item>
          <Form.Item name="sender" rules={[{ required: true }]}><Input placeholder="显示发件人" style={{ width: 200 }} /></Form.Item>
          <Form.Item><Button type="primary" htmlType="submit">保存</Button></Form.Item>
        </Form>
        <Space.Compact style={{ marginTop: 12 }}>
          <Input id="test_to" placeholder="测试收件邮箱" style={{ width: 260 }} />
          <Button loading={testing} onClick={test}>发送测试邮件</Button>
        </Space.Compact>
      </Card>
    </Space>
  );
}

function SmsTab() {
  const { rows, reload } = useChannels();
  const [form] = Form.useForm();
  const submit = async (v: Record<string, unknown>) => {
    try {
      await notifyApi.putSms({ ...v, enabled: true } as never);
      message.success("已保存并热生效");
      reload();
    } catch (e) {
      message.error(String(e));
    }
  };
  return (
    <Space direction="vertical" size="large" style={{ width: "100%" }}>
      <ChannelTable rows={rows.filter((r) => r.channel === "sms")} />
      <Card title="配置短信服务商（aliyun 已实装；tencent/volc/baidu 预留，采购后 U2 转正）">
        <Form form={form} layout="inline" onFinish={submit} initialValues={{ provider: "aliyun", name: "default" }}>
          <Form.Item name="name" rules={[{ required: true }]}><Input placeholder="渠道名 default" /></Form.Item>
          <Form.Item name="provider"><Select style={{ width: 120 }} options={["aliyun", "tencent", "volc", "baidu"].map((v) => ({ value: v }))} /></Form.Item>
          <Form.Item name="ak_id" rules={[{ required: true }]}><Input.Password placeholder="AccessKey ID" style={{ width: 200 }} /></Form.Item>
          <Form.Item name="ak_secret" rules={[{ required: true }]}><Input.Password placeholder="AccessKey Secret" style={{ width: 200 }} /></Form.Item>
          <Form.Item name="sign" rules={[{ required: true }]}><Input placeholder="短信签名" style={{ width: 140 }} /></Form.Item>
          <Form.Item name="template" rules={[{ required: true }]}><Input placeholder="模板 code" style={{ width: 160 }} /></Form.Item>
          <Form.Item><Button type="primary" htmlType="submit">保存</Button></Form.Item>
        </Form>
      </Card>
    </Space>
  );
}

function ChannelTable({ rows }: { rows: ChannelRow[] }) {
  return (
    <Table<ChannelRow>
      rowKey="id"
      size="small"
      pagination={false}
      dataSource={rows}
      columns={[
        { title: "ID", dataIndex: "id", width: 60 },
        { title: "渠道", dataIndex: "channel", width: 80 },
        { title: "名称", dataIndex: "name", width: 120 },
        {
          title: "配置（脱敏）", dataIndex: "config",
          render: (c: Record<string, unknown>) => (
            <code style={{ fontSize: 12 }}>{JSON.stringify(maskConfig(c))}</code>
          ),
        },
        { title: "状态", dataIndex: "enabled", width: 80, render: (v: boolean) => (v ? <Tag color="green">启用</Tag> : <Tag>停用</Tag>) },
      ]}
    />
  );
}

function maskConfig(c: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(c ?? {})) {
    out[k] = typeof v === "string" && v.includes("***") ? v : v;
  }
  return out;
}

function LlmTab() {
  const [providers, setProviders] = useState<LlmProvider[]>([]);
  const [models, setModels] = useState<LlmModel[]>([]);
  const [pForm] = Form.useForm();
  const [mForm] = Form.useForm();
  const reload = useCallback(() => {
    llmApi.list().then((d) => {
      setProviders(d.providers);
      setModels(d.models);
    }).catch((e) => message.error(String(e)));
  }, []);
  useEffect(reload, [reload]);

  const addProvider = async (v: Record<string, unknown>) => {
    try {
      await llmApi.createProvider(v as never);
      message.success("提供商已加密入库");
      pForm.resetFields();
      reload();
    } catch (e) {
      message.error(String(e));
    }
  };
  const addModel = async (v: Record<string, unknown>) => {
    try {
      await llmApi.createModel({ ...v, provider_id: Number(v.provider_id), weight: Number(v.weight ?? 1) } as never);
      message.success("模型已入库");
      mForm.resetFields();
      reload();
    } catch (e) {
      message.error(String(e));
    }
  };
  const toggleProvider = async (p: LlmProvider) => {
    await llmApi.updateProvider(p.id, { enabled: !p.enabled });
    reload();
  };
  const toggleModel = async (m: LlmModel) => {
    await llmApi.updateModel(m.id, { enabled: !m.enabled });
    reload();
  };

  return (
    <Space direction="vertical" size="large" style={{ width: "100%" }}>
      <Card title="提供商（base_url + 凭据，密文入库）">
        <Form form={pForm} layout="inline" onFinish={addProvider}>
          <Form.Item name="name" rules={[{ required: true }]}><Input placeholder="ark|siliconflow|qwen|deepseek" style={{ width: 180 }} /></Form.Item>
          <Form.Item name="base_url" rules={[{ required: true }]}><Input placeholder="https://.../v3" style={{ width: 320 }} /></Form.Item>
          <Form.Item name="api_key" rules={[{ required: true }]}><Input.Password placeholder="API Key" style={{ width: 220 }} /></Form.Item>
          <Form.Item><Button type="primary" htmlType="submit">添加</Button></Form.Item>
        </Form>
        <Table<LlmProvider>
          rowKey="id" size="small" pagination={false} style={{ marginTop: 12 }}
          dataSource={providers}
          columns={[
            { title: "ID", dataIndex: "id", width: 50 },
            { title: "名称", dataIndex: "name", width: 120 },
            { title: "Base URL", dataIndex: "base_url" },
            { title: "Key", dataIndex: "api_key", width: 140 },
            {
              title: "操作", width: 90,
              render: (_, p) => <Button size="small" onClick={() => toggleProvider(p)}>{p.enabled ? "停用" : "启用"}</Button>,
            },
          ]}
        />
      </Card>
      <Card title="模型（轮询渠道 = 提供商 × 模型，weight 为轮询权重）">
        <Form form={mForm} layout="inline" onFinish={addModel} initialValues={{ model_type: "chat", weight: 1 }}>
          <Form.Item name="provider_id" rules={[{ required: true }]}>
            <Select style={{ width: 180 }} placeholder="提供商" options={providers.map((p) => ({ value: p.id, label: p.name }))} />
          </Form.Item>
          <Form.Item name="model_id" rules={[{ required: true }]}><Input placeholder="ark-code-latest" style={{ width: 200 }} /></Form.Item>
          <Form.Item name="model_name" rules={[{ required: true }]}><Input placeholder="展示名" style={{ width: 160 }} /></Form.Item>
          <Form.Item name="model_type"><Select style={{ width: 110 }} options={["chat", "embedding", "image"].map((v) => ({ value: v }))} /></Form.Item>
          <Form.Item name="weight"><InputNumber min={1} max={100} /></Form.Item>
          <Form.Item><Button type="primary" htmlType="submit">添加</Button></Form.Item>
        </Form>
        <Table<LlmModel>
          rowKey="id" size="small" pagination={false} style={{ marginTop: 12 }}
          dataSource={models}
          columns={[
            { title: "ID", dataIndex: "id", width: 50 },
            { title: "提供商", dataIndex: "provider_id", width: 80 },
            { title: "模型 ID", dataIndex: "model_id" },
            { title: "展示名", dataIndex: "model_name", width: 160 },
            { title: "类型", dataIndex: "model_type", width: 90 },
            { title: "权重", dataIndex: "weight", width: 70 },
            {
              title: "操作", width: 90,
              render: (_, m) => <Button size="small" onClick={() => toggleModel(m)}>{m.enabled ? "停用" : "启用"}</Button>,
            },
          ]}
        />
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          渠道启停/换 Key 后由 ai-query 30s TTL 自动生效；坏凭据(401/403)自动冷却 300s，连续失败 5 次冷却 60s 起指数退避。
        </Typography.Text>
      </Card>
    </Space>
  );
}
