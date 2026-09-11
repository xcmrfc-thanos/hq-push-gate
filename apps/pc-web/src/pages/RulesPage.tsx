import { MARKETS, MARKET_LABELS } from "@hq/shared";
import type { AlertRule } from "@hq/shared";
import { Button, Card, Empty, Form, InputNumber, Popconfirm, Select, Switch, Table, message } from "antd";
import { useEffect, useState } from "react";
import { apiClient } from "../services/socket";

const FALLBACK_RULE_TYPES = [
  { value: "PRICE_ABOVE", label: "价格高于" },
  { value: "PRICE_BELOW", label: "价格低于" },
  { value: "PRICE_RANGE", label: "价格区间" },
  { value: "PCT_CHANGE", label: "涨跌幅超过" },
  { value: "INDICATOR", label: "成交量突破" },
];

export function RulesPage() {
  const [rules, setRules] = useState<AlertRule[]>([]);
  const [loading, setLoading] = useState(true);
  const [form] = Form.useForm();
  // 类型选项 meta 驱动（B26：GET /ai/meta 的 rule_type 去重；失败回退本地清单）
  const [ruleTypes, setRuleTypes] = useState(FALLBACK_RULE_TYPES);

  useEffect(() => {
    apiClient.aiMeta().then((m) => {
      const seen = new Map<string, string>();
      for (const t of m.query_types) seen.set(t.rule_type, t.label);
      if (seen.has("INDICATOR")) seen.set("INDICATOR", "成交量突破");
      setRuleTypes(Array.from(seen, ([value, label]) => ({ value, label })));
    }).catch(() => {}); // meta 不可达时静默回退
  }, []);

  const reload = () => {
    setLoading(true);
    apiClient.listRules().then(setRules).catch(() => {}).finally(() => setLoading(false));
  };
  useEffect(() => { void reload(); }, []);

  const onCreate = async (v: {
    market: string; symbol: string; ruleType: string;
    threshold?: number; low?: number; high?: number; cooldownSec: number;
  }) => {
    let condition: Record<string, unknown>;
    if (v.ruleType === "PRICE_RANGE") {
      condition = { low: v.low, high: v.high };
    } else if (v.ruleType === "INDICATOR") {
      // T0 实时子集：仅 INDICATOR/VOLUME（ADR-042 §8 映射口径）
      condition = { indicator: "VOLUME", op: ">", value: v.threshold };
    } else {
      condition = { threshold: v.threshold };
    }
    await apiClient.createRule({
      market: v.market, symbol: v.symbol, ruleType: v.ruleType,
      condition: JSON.stringify(condition), cooldownSec: v.cooldownSec ?? 60, status: 1,
    });
    void message.success("规则已创建（经 outbox 广播到 Flink）");
    form.resetFields();
    void reload();
  };

  const onToggle = async (r: AlertRule, enabled: boolean) => {
    await apiClient.updateRuleStatus(r.id!, enabled ? 1 : 0);
    void reload();
  };

  const onDelete = async (r: AlertRule) => {
    await apiClient.deleteRule(r.id!);
    void reload();
  };

  return (
    <Card title="预警规则">
      <Form form={form} layout="inline" onFinish={onCreate} style={{ marginBottom: 16 }}>
        <Form.Item name="market" initialValue="A_SHARE" rules={[{ required: true }]}>
          <Select style={{ width: 100 }} options={MARKETS.map((m) => ({ value: m, label: MARKET_LABELS[m] }))} />
        </Form.Item>
        <Form.Item name="symbol" rules={[{ required: true, message: "代码" }]}>
          <Select style={{ width: 130 }} options={Array.from({ length: 10 }, (_, i) => `60000${i}`)
            .map((s) => ({ value: s, label: s }))} showSearch />
        </Form.Item>
        <Form.Item name="ruleType" initialValue="PRICE_ABOVE" rules={[{ required: true }]}>
          <Select style={{ width: 150 }} options={ruleTypes} />
        </Form.Item>
        <Form.Item noStyle shouldUpdate={(a, b) => a.ruleType !== b.ruleType}>
          {({ getFieldValue }) =>
            getFieldValue("ruleType") === "PRICE_RANGE" ? (
              <>
                <Form.Item name="low" rules={[{ required: true }]}>
                  <InputNumber placeholder="下限" min={0} />
                </Form.Item>
                <Form.Item name="high" rules={[{ required: true }]}>
                  <InputNumber placeholder="上限" min={0} />
                </Form.Item>
              </>
            ) : (
              <Form.Item name="threshold" rules={[{ required: true }]}>
                <InputNumber placeholder="阈值" min={0} />
              </Form.Item>
            )
          }
        </Form.Item>
        <Form.Item name="cooldownSec" initialValue={60}>
          <InputNumber placeholder="冷却(秒)" min={0} style={{ width: 110 }} />
        </Form.Item>
        <Button type="primary" htmlType="submit">新增</Button>
      </Form>

      <Table
        rowKey="id"
        size="middle"
        loading={loading}
        pagination={false}
        locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="还没有规则，用上方表单创建第一条预警" /> }}
        dataSource={rules}
        columns={[
          { title: "市场", dataIndex: "market", render: (m: string) => MARKET_LABELS[m as keyof typeof MARKET_LABELS] ?? m },
          { title: "代码", dataIndex: "symbol" },
          { title: "类型", dataIndex: "ruleType", render: (t: string) => ruleTypes.find((x) => x.value === t)?.label ?? t },
          { title: "条件", dataIndex: "condition" },
          { title: "冷却(秒)", dataIndex: "cooldownSec" },
          { title: "版本", dataIndex: "version" },
          { title: "启用", dataIndex: "status", render: (s: number, r) => (
            <Switch checked={s === 1} onChange={(v) => onToggle(r, v)} />
          ) },
          { title: "操作", render: (_, r) => (
            <Popconfirm title="删除该规则？" onConfirm={() => onDelete(r)}>
              <Button danger size="small">删除</Button>
            </Popconfirm>
          ) },
        ]}
      />
    </Card>
  );
}
