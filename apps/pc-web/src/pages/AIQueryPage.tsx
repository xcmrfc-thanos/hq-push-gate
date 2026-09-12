import { MARKETS, MARKET_LABELS, conditionToRuleInput } from "@hq/shared";
import type { AIMeta, AIQueryResp } from "@hq/shared";
import { Alert, Button, Card, Input, Select, Space, Table, Tag, Tooltip, Typography, message } from "antd";
import { useCallback, useEffect, useState } from "react";
import { ConditionBuilder, groupChildren, isGroupNode, validateNode } from "../components/ConditionBuilder";
import type { CondNode } from "../components/ConditionBuilder";
import { apiClient } from "../services/socket";

/** 选股工作台（B29）：组件式条件编排器为主入口（拖拉积木组条件，白名单 schema v2），
 * AI 一句话生成条件一键填入继续编辑；立即选股走 /ai/screen 条件直查（无 LLM）；
 * 单票订阅：单条件透传 /rules（组合条件规则侧扁平，仅查询不支持订阅）。 */
export function AIQueryPage() {
  const [meta, setMeta] = useState<AIMeta | null>(null);
  const [metaError, setMetaError] = useState(false);
  const [cond, setCond] = useState<CondNode | null>(null);
  const [market, setMarket] = useState("A_SHARE");
  const [nl, setNl] = useState("");
  const [nlLoading, setNlLoading] = useState(false);
  const [screenLoading, setScreenLoading] = useState(false);
  const [resp, setResp] = useState<AIQueryResp | null>(null);

  const loadMeta = useCallback(() => {
    setMetaError(false);
    apiClient.aiMeta().then(setMeta).catch(() => setMetaError(true));
  }, []);
  useEffect(loadMeta, [loadMeta]);

  const runScreen = async (node: CondNode | null) => {
    if (!node) return void message.warning("先添加至少一个条件");
    if (!meta) return;
    const errs = validateNode(node, meta);
    if (errs.length) return void message.warning(errs[0]);
    setScreenLoading(true);
    try {
      setResp(await apiClient.aiScreen(node, 50));
    } catch (e) {
      void message.error((e as Error).message);
    } finally {
      setScreenLoading(false);
    }
  };

  const aiFill = async () => {
    if (!nl.trim()) return void message.warning("输入自然语言描述，AI 生成条件后可继续用积木调整");
    setNlLoading(true);
    try {
      const r = await apiClient.aiQuery(nl.trim(), 10);
      setCond(r.condition);
      message.success("条件已生成并填入编辑器，可继续调整后立即选股");
      await runScreen(r.condition);
    } catch (e) {
      void message.error((e as Error).message);
    } finally {
      setNlLoading(false);
    }
  };

  const subscribe = async (symbol: string) => {
    if (!resp || !cond) return;
    const mapped = conditionToRuleInput(cond, market, symbol);
    if (!mapped.ok) return void message.warning(mapped.reason);
    try {
      await apiClient.createRule(mapped.rule);
      void message.success(`${symbol} 预警规则已创建（经 outbox 广播到 Flink）`);
    } catch (e) {
      void message.error((e as Error).message);
    }
  };

  const errs = cond && meta ? validateNode(cond, meta) : [];
  const isGroup = cond ? isGroupNode(cond) : false;

  return (
    <Space direction="vertical" size="large" style={{ width: "100%" }}>
      <Card
        title="选股工作台"
        extra={
          <Select value={market} onChange={setMarket} style={{ width: 110 }}
            options={MARKETS.map((m) => ({ value: m, label: MARKET_LABELS[m] }))} />
        }
      >
        {metaError && (
          <Alert type="warning" showIcon style={{ marginBottom: 12 }}
            message="白名单元数据加载失败" action={<Button size="small" onClick={loadMeta}>重试</Button>} />
        )}
        <Space.Compact style={{ width: "100%", marginBottom: 10 }}>
          <Input
            placeholder="AI 辅助：用一句话描述，生成条件后可继续用下方积木调整"
            value={nl} onChange={(e) => setNl(e.target.value)}
            onPressEnter={() => void aiFill()} maxLength={500}
          />
          <Button loading={nlLoading} onClick={() => void aiFill()}>AI 生成条件</Button>
        </Space.Compact>

        {!meta ? (
          <Typography.Text type="secondary">条件块加载中…（需要 /ai/meta）</Typography.Text>
        ) : (
          <ConditionBuilder meta={meta} value={cond}
            onChange={setCond} onRemove={() => { setCond(null); setResp(null); }} />
        )}

        <Space style={{ marginTop: 12 }}>
          <Button type="primary" loading={screenLoading} onClick={() => void runScreen(cond)}>
            立即选股
          </Button>
          {errs.length > 0 && <Typography.Text type="danger">{errs[0]}</Typography.Text>}
        </Space>
      </Card>

      {resp && (
        <Card title="选股结果" extra={
          <Space>
            <Tag color="blue">{JSON.stringify(resp.condition)}</Tag>
            {resp.cached && <Tag color="green">缓存</Tag>}
          </Space>
        }>
          <Table
            rowKey="symbol" size="small" pagination={{ pageSize: 10 }}
            dataSource={resp.symbols}
            locale={{ emptyText: "没有匹配的标的" }}
            columns={[
              { title: "代码", dataIndex: "symbol" },
              { title: "最新价", dataIndex: "last" },
              { title: "涨跌幅%", dataIndex: "pct" },
              { title: "成交量", dataIndex: "volume", render: (v: number) => v.toLocaleString() },
              {
                title: "订阅预警", width: 130,
                render: (_: unknown, row) => (
                  <Tooltip title={isGroup ? "组合条件暂不支持订阅：可按单一条件逐块创建" : "以当前条件为此票创建预警规则"}>
                    <Button size="small" type="primary" ghost disabled={isGroup}
                      onClick={() => void subscribe(row.symbol)}>
                      订阅
                    </Button>
                  </Tooltip>
                ),
              },
            ]}
          />
        </Card>
      )}
    </Space>
  );
}
