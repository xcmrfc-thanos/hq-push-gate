import { MARKETS, MARKET_LABELS, conditionToRuleInput } from "@hq/shared";
import type { AIQueryResp } from "@hq/shared";
import { Button, Card, Input, Select, Space, Table, Tag, Tooltip, Typography, message } from "antd";
import { useState } from "react";
import { apiClient } from "../services/socket";

/** AI 选股（B26）：自然语言 → 条件 → 结果集 → 单票订阅（订阅 = condition 透传 POST /rules，
 * VOLUME_ABOVE 映射 INDICATOR/VOLUME；组合条件规则侧扁平不支持，按钮禁用并提示）。 */
export function AIQueryPage() {
  const [q, setQ] = useState("");
  const [loading, setLoading] = useState(false);
  const [resp, setResp] = useState<AIQueryResp | null>(null);
  const [market, setMarket] = useState("A_SHARE");

  const run = async () => {
    if (!q.trim()) return void message.warning("输入你的筛选条件，如：涨幅超过5%且股价高于10元");
    setLoading(true);
    try {
      setResp(await apiClient.aiQuery(q.trim(), 50));
    } catch (e) {
      void message.error((e as Error).message);
    } finally {
      setLoading(false);
    }
  };

  const subscribe = async (symbol: string) => {
    if (!resp) return;
    const mapped = conditionToRuleInput(resp.condition, market, symbol);
    if (!mapped.ok) return void message.warning(mapped.reason);
    try {
      await apiClient.createRule(mapped.rule);
      void message.success(`${symbol} 规则已创建（经 outbox 广播到 Flink）`);
    } catch (e) {
      void message.error((e as Error).message);
    }
  };

  const conditionText = resp ? JSON.stringify(resp.condition) : "";
  const isGroup = resp ? "all" in resp.condition || "any" in resp.condition : false;

  return (
    <Card title="AI 选股">
      <Space.Compact style={{ width: "100%", marginBottom: 12 }}>
        <Select value={market} onChange={setMarket} style={{ width: 110 }}
          options={MARKETS.map((m) => ({ value: m, label: MARKET_LABELS[m] }))} />
        <Input
          placeholder="自然语言筛选，如：涨幅超过5%且股价高于10元"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          onPressEnter={run}
          maxLength={500}
        />
        <Button type="primary" loading={loading} onClick={run}>查询</Button>
      </Space.Compact>

      {resp && (
        <>
          <Space style={{ marginBottom: 8 }}>
            <Typography.Text type="secondary">解析条件：</Typography.Text>
            <Tag color="blue">{conditionText}</Tag>
            <Tag>v{resp.condition_version}</Tag>
            {resp.cached && <Tag color="green">缓存命中</Tag>}
            {isGroup && <Tag color="orange">组合条件：结果可查，订阅请对单只票使用单一条件</Tag>}
          </Space>
          <Table
            rowKey="symbol"
            size="small"
            pagination={{ pageSize: 10 }}
            dataSource={resp.symbols}
            columns={[
              { title: "代码", dataIndex: "symbol" },
              { title: "最新价", dataIndex: "last" },
              { title: "涨跌幅%", dataIndex: "pct" },
              { title: "成交量", dataIndex: "volume", render: (v: number) => v.toLocaleString() },
              {
                title: "订阅预警", width: 120,
                render: (_: unknown, row) => (
                  <Tooltip title={isGroup ? "组合条件暂不支持订阅" : "以当前条件为此票创建预警规则"}>
                    <Button size="small" type="primary" ghost disabled={isGroup}
                      onClick={() => void subscribe(row.symbol)}>
                      订阅
                    </Button>
                  </Tooltip>
                ),
              },
            ]}
          />
        </>
      )}
    </Card>
  );
}
