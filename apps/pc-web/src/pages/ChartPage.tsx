import type { QuotePush } from "@hq/shared";
import { splitSymbol } from "@hq/shared";
import { dispose, init } from "klinecharts";
import type { Chart, DeepPartial, KLineData, Styles } from "klinecharts";
import { Card, Select, Spin, Typography } from "antd";
import { useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { apiClient, getSocket } from "../services/socket";
import { useQuoteStore } from "../stores/quoteStore";
import { useUiStore } from "../stores/uiStore";
import { BRAND } from "../app/theme";

/** K线图主题样式：红涨绿跌 + 随亮暗模式联动的网格/文字/十字光标。 */
function chartStyles(dark: boolean): DeepPartial<Styles> {
  const text = dark ? "#9aa4b2" : "#6b7280";
  const gridLine = dark ? "rgba(148,163,184,0.12)" : "rgba(100,116,139,0.14)";
  return {
    grid: {
      show: true,
      horizontal: { show: true, color: gridLine, size: 1, style: "dashed", dashedValue: [4, 4] },
      vertical: { show: true, color: gridLine, size: 1, style: "dashed", dashedValue: [4, 4] },
    },
    candle: {
      bar: {
        upColor: BRAND.up,
        downColor: BRAND.down,
        noChangeColor: BRAND.flat,
        upBorderColor: BRAND.up,
        downBorderColor: BRAND.down,
        noChangeBorderColor: BRAND.flat,
        upWickColor: BRAND.up,
        downWickColor: BRAND.down,
        noChangeWickColor: BRAND.flat,
      },
    },
    xAxis: { tickText: { color: text }, axisLine: { color: gridLine } },
    yAxis: { tickText: { color: text }, axisLine: { color: gridLine } },
    crosshair: {
      horizontal: { line: { color: gridLine }, text: { color: text } },
      vertical: { line: { color: gridLine }, text: { color: text } },
    },
    indicator: {
      lines: [{ color: BRAND.primary, size: 1, style: "solid" as const, dashedValue: [] }],
    },
  };
}

/**
 * K线页：历史首屏走 /quote/kline（ClickHouse 归档，docs/04 §6 既定流程），
 * 实时增量由 WS 行情在客户端聚合为 1m bar（klinecharts 10 通过 DataLoader 接入）。
 */
export function ChartPage() {
  const [params] = useSearchParams();
  const [symbolKey, setSymbolKey] = useState(params.get("symbol") ?? "A_SHARE:600000");
  const chartRef = useRef<HTMLDivElement>(null);
  const chartInstRef = useRef<Chart | null>(null);
  const candlesRef = useRef<KLineData[]>([]);
  const barCbRef = useRef<((bar: KLineData) => void) | null>(null);
  const [ready, setReady] = useState(0);
  const [hasBar, setHasBar] = useState(false);

  useEffect(() => {
    if (!chartRef.current) return;
    const c = init(chartRef.current);
    if (!c) return;
    c.setStyles(chartStyles(useUiStore.getState().mode === "dark"));
    chartInstRef.current = c;
    setReady((v) => v + 1);
    return () => {
      barCbRef.current = null;
      dispose(chartRef.current!);
      chartInstRef.current = null;
    };
  }, []);

  // 暗色模式切换时同步图表配色
  const mode = useUiStore((s) => s.mode);
  useEffect(() => {
    chartInstRef.current?.setStyles(chartStyles(mode === "dark"));
  }, [mode]);

  // 标的切换：拉取历史首屏（/quote/kline，1m 基期）并重新挂载数据源
  const [loadingHist, setLoadingHist] = useState(false);
  useEffect(() => {
    const c = chartInstRef.current;
    if (!c || ready === 0) return;
    candlesRef.current = [];
    setHasBar(false);
    const { market, symbol: code } = splitSymbol(symbolKey);
    setLoadingHist(true);
    apiClient
      .kline(market, code, 1, 500)
      .then((bars) => {
        candlesRef.current = (bars ?? []).map((b) => ({
          timestamp: b.t,
          open: b.o,
          high: b.h,
          low: b.l,
          close: b.c,
          volume: b.v,
        }));
      })
      .catch(() => {})
      .finally(() => {
        setLoadingHist(false);
        setHasBar(candlesRef.current.length > 0);
      });
    c.setDataLoader({
      getBars: ({ callback }) => callback([...candlesRef.current]),
      subscribeBar: ({ callback }) => {
        barCbRef.current = callback;
        const last = candlesRef.current[candlesRef.current.length - 1];
        if (last) callback(last);
      },
      unsubscribeBar: () => {
        barCbRef.current = null;
      },
    });
  }, [symbolKey, ready]);

  // 订阅当前标的
  useEffect(() => {
    const s = getSocket();
    s.subscribe([symbolKey]);
    return () => s.unsubscribe([symbolKey]);
  }, [symbolKey]);

  // 行情 -> 1m K 线增量聚合：REST 历史为每根 bar 成交量，实时侧按快照累计量求差累加
  const lastVolRef = useRef<number | null>(null);
  const quotes = useQuoteStore((s) => s.quotes);
  useEffect(() => {
    const q: QuotePush | undefined = quotes[symbolKey];
    if (!q) return;
    const bucketTs = Math.floor(q.ts / 60000) * 60000;
    const candles = candlesRef.current;
    const last = candles[candles.length - 1];
    let bar: KLineData;
    if (!last || last.timestamp !== bucketTs) {
      bar = { timestamp: bucketTs, open: q.last, high: q.last, low: q.last, close: q.last, volume: 0 };
      candles.push(bar);
      if (candles.length > 500) candles.shift();
      lastVolRef.current = q.vol;
    } else {
      const delta = lastVolRef.current == null ? 0 : Math.max(0, q.vol - lastVolRef.current);
      lastVolRef.current = q.vol;
      last.high = Math.max(last.high, q.last);
      last.low = Math.min(last.low, q.last);
      last.close = q.last;
      last.volume = (last.volume ?? 0) + delta;
      bar = last;
    }
    setHasBar(true);
    barCbRef.current?.(bar);
  }, [symbolKey, quotes]);
  const { symbol: code } = splitSymbol(symbolKey);

  return (
    <Card
      title={<Typography.Text strong>{code} · 1分钟（历史 + 实时）</Typography.Text>}
      extra={
        <Select
          style={{ width: 140 }}
          value={symbolKey}
          onChange={setSymbolKey}
          options={Array.from({ length: 10 }, (_, i) => `A_SHARE:60000${i}`)
            .map((s) => ({ value: s, label: s.split(":")[1] }))}
        />
      }
    >
      <div style={{ position: "relative" }}>
        <div ref={chartRef} style={{ height: 480 }} />
        <Spin
          spinning={loadingHist || !hasBar}
          tip="加载K线（历史 + 实时聚合）…"
          size="large"
          style={{ position: "absolute", inset: 0, display: "grid", placeItems: "center", maxHeight: 480 }}
        >
          <div style={{ height: 480 }} />
        </Spin>
      </div>
    </Card>
  );
}
