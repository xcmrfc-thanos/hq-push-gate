import { Button, Input, List, Picker, Radio, Selector, Toast } from "antd-mobile";
import type { ComponentProps } from "react";
import { useEffect, useState } from "react";
import { conditionToRuleInput } from "@hq/shared";
import type { AIMeta, AIQueryResp, AITypeMeta } from "@hq/shared";
import { apiClient } from "../services/socket";

interface LeafDraft {
  meta: AITypeMeta;
  values: Record<string, number | string | undefined>;
}

/** AI 选股轻编排（B29）：条件行组件式增删 + 且/或切换（移动端不做嵌套组），
 * 编译为 schema v2 条件 → /ai/screen 直查；订阅与 pc 同源映射。 */
export function AIScreenPage() {
  const [meta, setMeta] = useState<AIMeta | null>(null);
  const [items, setItems] = useState<LeafDraft[]>([]);
  const [joiner, setJoiner] = useState<"all" | "any">("all");
  const [loading, setLoading] = useState(false);
  const [resp, setResp] = useState<AIQueryResp | null>(null);
  const [pickerIdx, setPickerIdx] = useState<number | null>(null);

  useEffect(() => {
    apiClient.aiMeta().then((m) => {
      setMeta(m);
      setItems([{ meta: m.query_types[0], values: {} }]);
    }).catch((e) => Toast.show({ content: `meta 加载失败: ${(e as Error).message}` }));
  }, []);

  const compile = (): Record<string, unknown> | null => {
    if (!meta || items.length === 0) return null;
    const leaves = items.map(({ meta: tm, values }) => {
      const cond: Record<string, unknown> = { type: tm.type };
      for (const f of tm.fields) {
        const v = values[f.name];
        if (v !== undefined && v !== "") cond[f.name] = typeof v === "string" ? v : Number(v);
      }
      return cond;
    });
    return leaves.length === 1 ? leaves[0] : { [joiner]: leaves };
  };

  const run = async () => {
    const condition = compile();
    if (!condition) return void Toast.show({ content: "先添加条件" });
    setLoading(true);
    try {
      setResp(await apiClient.aiScreen(condition, 20));
    } catch (e) {
      Toast.show({ content: (e as Error).message });
    } finally {
      setLoading(false);
    }
  };

  const subscribe = async (symbol: string) => {
    const condition = compile();
    if (!condition) return;
    const mapped = conditionToRuleInput(condition, "A_SHARE", symbol);
    if (!mapped.ok) return void Toast.show({ content: mapped.reason });
    try {
      await apiClient.createRule(mapped.rule);
      Toast.show({ content: `${symbol} 预警已创建` });
    } catch (e) {
      Toast.show({ content: (e as Error).message });
    }
  };

  const isGroup = items.length > 1;
  const typeColumns: ComponentProps<typeof Picker>["columns"] = [
    (meta?.query_types ?? []).map((t) => ({ label: t.label, value: t.type })),
  ];

  return (
    <div style={{ padding: 12 }}>
      {meta && (
        <div style={{ marginBottom: 10 }}>
          {items.map((it, idx) => (
            <div key={idx} style={{ display: "flex", gap: 6, marginBottom: 8, alignItems: "center", flexWrap: "wrap" }}>
              <Button size="mini" fill="outline" onClick={() => setPickerIdx(idx)}>
                {it.meta.label}
              </Button>
              {it.meta.fields.filter((f) => f.type === "float").map((f) => (
                <Input
                  key={f.name} inputMode="decimal" placeholder={f.label}
                  value={it.values[f.name] === undefined ? "" : String(it.values[f.name])}
                  onChange={(v) => setItems(items.map((x, i) => (
                    i === idx ? { ...x, values: { ...x.values, [f.name]: v === "" ? undefined : v } } : x)))}
                  style={{ width: 92 }}
                />
              ))}
              {it.meta.type === "PCT_CHANGE" && (
                <Selector
                  options={[{ label: "涨", value: "up" }, { label: "跌", value: "down" }, { label: "±", value: "both" }]}
                  value={[(it.values.direction as string) ?? "both"]}
                  onChange={(v) => setItems(items.map((x, i) => (
                    i === idx ? { ...x, values: { ...x.values, direction: v[v.length - 1] } } : x)))}
                />
              )}
              <Button size="mini" fill="none" color="danger"
                onClick={() => setItems(items.filter((_, i) => i !== idx))}>✕</Button>
            </div>
          ))}
          <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
            <Button size="mini" color="primary" fill="outline"
              disabled={items.length >= 10}
              onClick={() => setItems([...items, { meta: meta.query_types[0], values: {} }])}>
              + 条件
            </Button>
            {items.length > 1 && (
              <Radio.Group value={joiner} onChange={(v) => setJoiner(v as "all" | "any")}>
                <Radio value="all" style={{ "--font-size": "13px" }}>且</Radio>
                <Radio value="any" style={{ "--font-size": "13px" }}>或</Radio>
              </Radio.Group>
            )}
          </div>
        </div>
      )}
      <Button block color="primary" loading={loading} onClick={run}>立即选股</Button>

      {resp && (
        <>
          <div style={{ fontSize: 12, color: "#888", margin: "8px 0", wordBreak: "break-all" }}>
            条件：{JSON.stringify(resp.condition)}{isGroup ? "｜组合条件不支持订阅" : ""}
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

      <Picker
        visible={pickerIdx !== null}
        onClose={() => setPickerIdx(null)}
        value={pickerIdx !== null ? [items[pickerIdx].meta.type] : []}
        columns={typeColumns}
        onConfirm={(val) => {
          if (pickerIdx === null) return;
          const first = val[0];
          if (typeof first !== "string") return;
          const tm = meta!.query_types.find((x) => x.type === first)!;
          setItems(items.map((x, i) => (i === pickerIdx ? { meta: tm, values: {} } : x)));
          setPickerIdx(null);
        }}
      />
    </div>
  );
}
