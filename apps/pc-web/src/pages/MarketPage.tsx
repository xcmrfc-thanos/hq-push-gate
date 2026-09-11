import { MARKETS, MARKET_LABELS, fmtVol, splitSymbol } from "@hq/shared";
import type { Market } from "@hq/shared";
import { Button, Card, Empty, Select, Table, Tag } from "antd";
import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { apiClient, getSocket } from "../services/socket";
import { useQuoteStore } from "../stores/quoteStore";
import { PriceCell } from "../components/PriceCell";

/** 默认自选池：模拟源会生成这些标的 */
const DEFAULT_SYMBOLS = Array.from({ length: 10 }, (_, i) => `60000${i}`);

export function MarketPage() {
  const nav = useNavigate();
  const [market, setMarket] = useState<Market>("A_SHARE");
  const [subscribed, setSubscribed] = useState<string[]>([]);
  const [firstLoad, setFirstLoad] = useState(true);
  const quotes = useQuoteStore((s) => s.quotes);
  const upsert = useQuoteStore((s) => s.upsert);
  const remove = useQuoteStore((s) => s.remove);

  // 首次进入：REST 快照 + WS 订阅（docs/04 §7-3）
  useEffect(() => {
    const keys = DEFAULT_SYMBOLS.map((s) => `${market}:${s}`);
    setFirstLoad(true);
    apiClient.snapshot(market, DEFAULT_SYMBOLS).then((rows) => {
      upsert(
        rows.map((r, i) => ({
          symbol: `${market}:${DEFAULT_SYMBOLS[i]}`,
          last: Number(r.last),
          pct: Number(r.pct),
          vol: Number(r.vol),
          ts: Number(r.ts),
        })),
      );
    }).catch(() => {}).finally(() => setFirstLoad(false));
    getSocket().subscribe(keys);
    setSubscribed((prev) => [...new Set([...prev, ...keys])]);
    return () => {
      getSocket().unsubscribe(keys);
      remove(keys);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [market]);

  const rows = Object.entries(quotes)
    .filter(([k]) => k.startsWith(market + ":"))
    .map(([key, q]) => ({ key, ...q, ...splitSymbol(key) }));

  return (
    <Card
      title="实时行情"
      extra={
        <Select value={market} style={{ width: 120 }} onChange={(m) => setMarket(m)} options={
          MARKETS.map((m) => ({ value: m, label: MARKET_LABELS[m] }))
        } />
      }
    >
      <Table
        size="middle"
        pagination={false}
        loading={firstLoad && rows.length === 0}
        locale={{
          emptyText: (
            <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无行情（确认 tick-source 已启动并订阅后自动推送）" />
          ),
        }}
        dataSource={rows}
        columns={[
          { title: "代码", dataIndex: "symbol", render: (v: string, r) => (
            <Button type="link" style={{ paddingInline: 0 }} onClick={() => nav(`/chart?symbol=${r.market}:${r.symbol}`)}>{v}</Button>
          ) },
          { title: "最新价", dataIndex: "last", render: (v: number) => <PriceCell value={v} /> },
          { title: "涨跌幅", dataIndex: "pct", render: (v: number) => (
            <span className="num" style={{ fontWeight: 600, color: v > 0 ? "#cf1322" : v < 0 ? "#3f8600" : undefined }}>
              {v > 0 ? "+" : ""}{v.toFixed(2)}%
            </span>
          ) },
          { title: "成交量", dataIndex: "vol", render: (v: number) => fmtVol(v) },
          { title: "时间", dataIndex: "ts", render: (v: number) => new Date(v).toLocaleTimeString() },
          { title: "状态", render: (_, r) => (
            subscribed.includes(r.key) ? <Tag color="green">已订阅</Tag> : null
          ) },
        ]}
      />
    </Card>
  );
}
