import { fmtPrice, fmtVol } from "@hq/shared";
import { List, PullToRefresh, Toast } from "antd-mobile";
import { useEffect } from "react";
import { apiClient, getSocket } from "../services/socket";
import { useQuoteStore } from "../stores/stores";

export function QuotesPage() {
  const quotes = useQuoteStore((s) => s.quotes);
  const upsert = useQuoteStore((s) => s.upsert);

  const keys = Array.from({ length: 10 }, (_, i) => `A_SHARE:60000${i}`);

  // REST 快照刷新（WS 推送为主，快照兜底校准）
  const refresh = async () => {
    try {
      const rows = await apiClient.snapshot("A_SHARE", keys.map((k) => k.split(":")[1]));
      upsert(rows.map((r, i) => ({
        symbol: keys[i], last: Number(r.last), pct: Number(r.pct), vol: Number(r.vol), ts: Number(r.ts),
      })));
    } catch {
      Toast.show({ content: "刷新失败，将随推送自动恢复" });
    }
  };

  useEffect(() => {
    getSocket().subscribe(keys);
    void refresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const rows = Object.values(quotes);

  return (
    <div className="fade-in">
      <PullToRefresh onRefresh={refresh}>
        <List header="实时行情 · 下拉刷新">
          {rows.length === 0 && <List.Item>等待行情推送…</List.Item>}
          {rows.map((q) => (
            <List.Item
              key={q.symbol}
              extra={
                <span className="num" style={{
                  color: q.pct > 0 ? "#cf1322" : q.pct < 0 ? "#3f8600" : "#888",
                  fontWeight: 600,
                }}>
                  {q.pct > 0 ? "+" : ""}{q.pct.toFixed(2)}%
                </span>
              }
              description={`成交量 ${fmtVol(q.vol)} · ${new Date(q.ts).toLocaleTimeString()}`}
            >
              {q.symbol.split(":")[1]}
              <span className="num" style={{ marginLeft: 12, fontWeight: 600 }}>
                {fmtPrice(q.last)}
              </span>
            </List.Item>
          ))}
        </List>
      </PullToRefresh>
    </div>
  );
}
