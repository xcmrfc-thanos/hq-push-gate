import { Button, Input, List, Toast, Dialog } from "antd-mobile";
import { useState } from "react";
import { conditionToRuleInput } from "@hq/shared";
import type { AIQueryResp } from "@hq/shared";
import { apiClient } from "../services/socket";

/** AI 选股轻入口（B26）：自然语言 → 结果列表 → 单票订阅（映射逻辑与 pc 同源：@hq/shared）。 */
export function AIScreenPage() {
  const [q, setQ] = useState("");
  const [loading, setLoading] = useState(false);
  const [resp, setResp] = useState<AIQueryResp | null>(null);

  const run = async () => {
    if (!q.trim()) return void Toast.show({ content: "输入筛选条件，如：涨幅超过5%" });
    setLoading(true);
    try {
      setResp(await apiClient.aiQuery(q.trim(), 20));
    } catch (e) {
      Toast.show({ content: (e as Error).message });
    } finally {
      setLoading(false);
    }
  };

  const subscribe = async (symbol: string) => {
    if (!resp) return;
    const mapped = conditionToRuleInput(resp.condition, "A_SHARE", symbol);
    if (!mapped.ok) return void Toast.show({ content: mapped.reason });
    try {
      await apiClient.createRule(mapped.rule);
      await Dialog.alert({ content: `${symbol} 预警已创建`, confirmText: "好的" });
    } catch (e) {
      Toast.show({ content: (e as Error).message });
    }
  };

  const isGroup = resp ? "all" in resp.condition || "any" in resp.condition : false;

  return (
    <div style={{ padding: 12 }}>
      <div style={{ display: "flex", gap: 8, marginBottom: 12 }}>
        <Input
          placeholder="如：涨幅超过5%且股价高于10元"
          value={q}
          onChange={setQ}
          maxLength={500}
        />
        <Button color="primary" size="small" loading={loading} onClick={run}>查询</Button>
      </div>
      {resp && (
        <>
          <div style={{ fontSize: 12, color: "#888", marginBottom: 8, wordBreak: "break-all" }}>
            解析条件：{JSON.stringify(resp.condition)}{resp.cached ? "（缓存）" : ""}
            {isGroup ? "｜组合条件不支持订阅" : ""}
          </div>
          <List>
            {resp.symbols.map((r) => (
              <List.Item
                key={r.symbol}
                extra={
                  <Button size="mini" color="primary" fill="outline" disabled={isGroup}
                    onClick={() => void subscribe(r.symbol)}>
                    订阅
                  </Button>
                }
                description={`价 ${r.last}｜涨跌 ${r.pct}%｜量 ${r.volume.toFixed(0)}`}
              >
                {r.symbol}
              </List.Item>
            ))}
          </List>
          {resp.symbols.length === 0 && (
            <div style={{ textAlign: "center", color: "#999", padding: 24 }}>没有匹配的标的</div>
          )}
        </>
      )}
    </div>
  );
}
