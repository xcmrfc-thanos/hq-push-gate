package com.hqpush.flink.function;

import com.hqpush.contract.hqv1.Kline;
import com.hqpush.contract.hqv1.Tick;
import org.apache.flink.api.common.functions.AggregateFunction;

/**
 * 1m 窗口 OHLCV 聚合：窗口内第一笔定 open、极值定 high/low、末笔定 close、累加量额（docs/04 §1 Kline）。
 * 5m/15m/60m/日K 由查询侧从 1m 滚动聚合（ClickHouse），深历史 5m 由 Baostock 回填直接落库。
 */
public final class KlineAggregate implements AggregateFunction<Tick, KlineAggregate.Acc, Kline> {

    public static final int PERIOD_MIN = 1;

    /** 可变累加器：窗口首末笔与极值。 */
    public static class Acc {
        public int market;
        public String symbol = "";
        public long beginTsMs;
        public double open, high, low, close, volume, amount;
        public boolean seen;
    }

    @Override
    public Acc createAccumulator() {
        return new Acc();
    }

    @Override
    public Acc add(Tick t, Acc acc) {
        if (!acc.seen) {
            acc.seen = true;
            acc.market = t.getMarketValue();
            acc.symbol = t.getSymbol();
            acc.open = t.getLastPrice();
            acc.high = t.getLastPrice();
            acc.low = t.getLastPrice();
        }
        acc.close = t.getLastPrice();
        acc.high = Math.max(acc.high, t.getLastPrice());
        acc.low = Math.min(acc.low, t.getLastPrice());
        acc.volume += t.getVolume();
        acc.amount += t.getAmount();
        return acc;
    }

    @Override
    public Kline getResult(Acc acc) {
        return Kline.newBuilder()
                .setMarket(com.hqpush.contract.hqv1.Market.forNumber(acc.market))
                .setSymbol(acc.symbol)
                .setPeriodMin(PERIOD_MIN)
                .setBeginTsMs(acc.beginTsMs)
                .setOpen(acc.open)
                .setHigh(acc.high)
                .setLow(acc.low)
                .setClose(acc.close)
                .setVolume(acc.volume)
                .setAmount(acc.amount)
                .build();
    }

    @Override
    public Acc merge(Acc a, Acc b) {
        if (!a.seen) {
            return b;
        }
        if (!b.seen) {
            return a;
        }
        a.close = b.close;
        a.high = Math.max(a.high, b.high);
        a.low = Math.min(a.low, b.low);
        a.volume += b.volume;
        a.amount += b.amount;
        return a;
    }
}
