import { Button, Card, InputNumber, Popconfirm, Radio, Select, Space, Switch, Typography } from "antd";
import type { AIMeta, AITypeMeta } from "@hq/shared";

/** 条件编排器（B29）：受限积木画布——块类型/字段/边界全部由 /ai/meta 白名单驱动，
 * 编译产物 = schema v2 条件 JSON（查询直查 + 订阅映射共用）。组合深度 ≤2、单组 ≤10。 */

export type CondNode = Record<string, unknown>;

export function isGroup(n: CondNode): boolean {
  return "all" in n || "any" in n;
}

export function groupKey(n: CondNode): "all" | "any" {
  return "all" in n ? "all" : "any";
}

export function groupChildren(n: CondNode): CondNode[] {
  return (n[groupKey(n)] as CondNode[]) ?? [];
}

export const isGroupNode = (n: unknown): n is CondNode =>
  typeof n === "object" && n !== null && ("all" in n || "any" in n);

function defaultLeaf(meta: AIMeta): CondNode {
  return { type: meta.query_types[0].type };
}

export function validateNode(node: CondNode, meta: AIMeta, depth = 1): string[] {
  const errs: string[] = [];
  const grouped = "all" in node || "any" in node;
  if (grouped) {
    const key = groupKey(node);
    const children = groupChildren(node);
    if (depth > 2) errs.push("组合嵌套超过 2 层");
    if (children.length === 0) errs.push("组合条件不能为空");
    if (children.length > 10) errs.push("单组最多 10 个条件");
    for (const c of children) errs.push(...validateNode(c, meta, depth + 1));
    return errs;
  }
  const tm = meta.query_types.find((t) => t.type === node.type);
  if (!tm) {
    errs.push(`未知条件类型: ${String(node.type)}`);
    return errs;
  }
  for (const f of tm.fields) {
    const v = node[f.name];
    if (f.type === "float") {
      if (typeof v !== "number" || Number.isNaN(v)) {
        errs.push(`${tm.label}/${f.label}: 数值必填`);
        continue;
      }
      if (f.ne !== undefined && v === f.ne) errs.push(`${tm.label}/${f.label}: 不能为 ${f.ne}`);
      if (f.gt !== undefined && v <= f.gt) errs.push(`${tm.label}/${f.label}: 需 > ${f.gt}`);
      if (f.ale !== undefined && Math.abs(v) > f.ale) errs.push(`${tm.label}/${f.label}: 绝对值需 ≤ ${f.ale}`);
      if (f.le !== undefined && v > f.le) errs.push(`${tm.label}/${f.label}: 需 ≤ ${f.le}`);
    } else if (f.type === "enum" && v !== undefined && !f.values?.includes(String(v))) {
      errs.push(`${tm.label}/${f.label}: 取值非法`);
    }
  }
  if (node.type === "PCT_CHANGE" && node.limit === true) {
    const d = node.direction;
    if (d !== "up" && d !== "down") errs.push("涨跌停预警需明确方向 up/down");
  }
  return errs;
}

interface NodeProps {
  meta: AIMeta;
  node: CondNode;
  onChange: (n: CondNode) => void;
  onRemove?: () => void;
  depth: number;
}

function LeafCard({ meta, node, onChange }: NodeProps) {
  const tm: AITypeMeta | undefined = meta.query_types.find((t) => t.type === node.type);
  const setField = (name: string, v: unknown) => {
    const next = { ...node };
    if (v === undefined || v === "" || v === false) delete next[name];
    else next[name] = v;
    onChange(next);
  };
  return (
    <Space wrap size={4}>
      <Select
        size="small" style={{ width: 150 }} value={node.type}
        onChange={(t) => onChange({ type: t })}
        options={meta.query_types.map((t) => ({ value: t.type, label: t.label }))}
      />
      {tm?.fields.map((f) => {
        if (f.type === "float") {
          return (
            <InputNumber
              key={f.name} size="small" style={{ width: 120 }} placeholder={f.label}
              value={(node[f.name] as number) ?? undefined}
              min={f.gt} max={f.le}
              onChange={(v) => setField(f.name, typeof v === "number" ? v : undefined)}
            />
          );
        }
        if (f.type === "enum") {
          return (
            <Select
              key={f.name} size="small" style={{ width: 96 }} placeholder={f.label}
              value={(node[f.name] as string) ?? undefined}
              allowClear
              onChange={(v) => setField(f.name, v)}
              options={(f.values ?? []).map((v) => ({ value: v, label: v }))}
            />
          );
        }
        if (f.type === "bool") {
          return (
            <Switch
              key={f.name} size="small" checkedChildren={f.label}
              checked={node[f.name] === true}
              onChange={(v) => setField(f.name, v || undefined)}
            />
          );
        }
        return null;
      })}
    </Space>
  );
}

function GroupCard({ meta, node, onChange, onRemove, depth }: NodeProps) {
  const key = groupKey(node);
  const children = groupChildren(node);
  const setChildren = (list: CondNode[]) => onChange({ [key]: list });
  const joinerLabel = key === "all" ? "满足全部（且）" : "满足任一（或）";
  return (
    <Card
      size="small"
      style={{ borderColor: depth > 1 ? "#b8d4ff" : undefined }}
      title={
        <Space>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>条件组</Typography.Text>
          <Radio.Group
            size="small" value={key}
            onChange={(e) => onChange({ [e.target.value]: groupChildren(node) })}
            options={[{ value: "all", label: "满足全部(且)" }, { value: "any", label: "满足任一(或)" }]}
            optionType="button" buttonStyle="solid"
          />
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>{joinerLabel}</Typography.Text>
        </Space>
      }
      extra={
        onRemove && (
          <Popconfirm title="删除该条件组？" onConfirm={onRemove}>
            <Button size="small" danger type="text">删除组</Button>
          </Popconfirm>
        )
      }
    >
      <Space direction="vertical" size={6} style={{ width: "100%" }}>
        {children.map((child, i) =>
          isGroupNode(child) ? (
            <GroupCard
              key={i} meta={meta} node={child} depth={depth + 1}
              onChange={(n) => {
                const list = [...children];
                list[i] = n;
                setChildren(list);
              }}
              onRemove={() => setChildren(children.filter((_, j) => j !== i))}
            />
          ) : (
            <Space key={i} size={4} style={{ display: "flex" }}>
              <LeafCard
                meta={meta} node={child} depth={depth + 1}
                onChange={(n) => {
                  const list = [...children];
                  list[i] = n;
                  setChildren(list);
                }}
              />
              <Button size="small" type="text" danger
                onClick={() => setChildren(children.filter((_, j) => j !== i))}>✕</Button>
            </Space>
          ),
        )}
        <Space size={4}>
          <Button size="small" type="dashed" disabled={children.length >= 10}
            onClick={() => setChildren([...children, defaultLeaf(meta)])}>
            + 条件
          </Button>
          {depth < 2 && (
            <Button size="small" type="dashed" disabled={children.length >= 10}
              onClick={() => setChildren([...children, { all: [defaultLeaf(meta)] }])}>
              + 子组合
            </Button>
          )}
        </Space>
      </Space>
    </Card>
  );
}

export function ConditionBuilder({ meta, value, onChange, onRemove, depth = 1 }: {
  meta: AIMeta;
  value: CondNode | null;
  onChange: (v: CondNode | null) => void;
  onRemove?: () => void;
  depth?: number;
}) {
  if (value === null) {
    return (
      <Space>
        <Button type="dashed" onClick={() => onChange(defaultLeaf(meta))}>+ 添加条件</Button>
        <Button type="dashed" onClick={() => onChange({ all: [defaultLeaf(meta)] })}>+ 条件组</Button>
      </Space>
    );
  }
  if (isGroupNode(value)) {
    return <GroupCard meta={meta} node={value} onChange={onChange} onRemove={onRemove} depth={depth} />;
  }
  return (
    <Space direction="vertical" size={6} style={{ width: "100%" }}>
      <LeafCard meta={meta} node={value} onChange={onChange} depth={depth} />
      <Space size={4}>
        <Button size="small" type="dashed" onClick={() => onChange({ all: [value] })}>转为条件组（可叠加）</Button>
        {onRemove && (
          <Popconfirm title="清空条件？" onConfirm={onRemove}>
            <Button size="small" danger type="text">清空</Button>
          </Popconfirm>
        )}
      </Space>
    </Space>
  );
}
