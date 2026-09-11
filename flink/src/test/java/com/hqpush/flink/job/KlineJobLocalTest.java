package com.hqpush.flink.job;

import com.hqpush.contract.hqv1.Kline;
import com.hqpush.contract.hqv1.Market;
import com.hqpush.contract.hqv1.Tick;
import org.apache.flink.streaming.api.datastream.DataStream;
import org.apache.flink.streaming.api.environment.StreamExecutionEnvironment;
import org.junit.jupiter.api.Test;

import java.util.Comparator;
import java.util.List;
import java.util.stream.Collectors;

import static org.junit.jupiter.api.Assertions.assertEquals;

/**
 * KlineJob 聚合段本地运行验证（MiniCluster，无 Kafka/容器依赖）：
 * 有界集合 → 1m 窗口聚合 → 收集结果断言 OHLCV 与 begin_ts 对齐（U1 定版验收项）。
 */
class KlineJobLocalTest {

    private static Tick tick(String symbol, long tsMs, double price, double vol, double amount) {
        return Tick.newBuilder()
                .setMarket(Market.A_SHARE)
                .setSymbol(symbol)
                .setExchange("SSE")
                .setTimestampMs(tsMs)
                .setLastPrice(price)
                .setVolume(vol)
                .setAmount(amount)
                .build();
    }

    /** 有界集合源完成时自动发 MAX_WATERMARK，触发全部窗口闭合。 */
    private static List<Kline> run(List<Tick> ticks) throws Exception {
        StreamExecutionEnvironment env = StreamExecutionEnvironment.getExecutionEnvironment();
        env.setParallelism(1);
        DataStream<Tick> source = env.fromCollection(ticks)
                .assignTimestampsAndWatermarks(
                        org.apache.flink.api.common.eventtime.WatermarkStrategy
                                .<Tick>forMonotonousTimestamps()
                                .withTimestampAssigner((t, ts) -> t.getTimestampMs()));
        return KlineJob.buildKlines(source).executeAndCollect("kline-it", 100);
    }

    @Test
    void aggregatesOneMinuteWindow() throws Exception {
        long minute0 = 1788505080000L; // 对齐分钟的事件时间基点
        long minute1 = minute0 + 60_000;
        List<Kline> bars = run(List.of(
                tick("600519", minute0 + 1_000, 10.0, 100, 1_000),
                tick("600519", minute0 + 20_000, 12.0, 200, 2_400),
                tick("600519", minute0 + 40_000, 9.0, 300, 2_700),
                tick("600519", minute1 + 1_000, 11.0, 50, 550)
        ));

        assertEquals(2, bars.size());
        bars.sort(Comparator.comparingLong(Kline::getBeginTsMs));

        Kline b0 = bars.get(0);
        assertEquals(minute0, b0.getBeginTsMs());
        assertEquals(10.0, b0.getOpen());
        assertEquals(12.0, b0.getHigh());
        assertEquals(9.0, b0.getLow());
        assertEquals(9.0, b0.getClose());
        assertEquals(600.0, b0.getVolume());
        assertEquals(6_100.0, b0.getAmount());
        assertEquals(1, b0.getPeriodMin());
        assertEquals("600519", b0.getSymbol());

        Kline b1 = bars.get(1);
        assertEquals(minute1, b1.getBeginTsMs());
        assertEquals(11.0, b1.getClose());
        assertEquals(50.0, b1.getVolume());
    }

    @Test
    void separatesSymbolsIntoIndependentWindows() throws Exception {
        long minute0 = 1788505080000L;
        List<Kline> bars = run(List.of(
                tick("600519", minute0 + 1_000, 10.0, 100, 1_000),
                tick("000001", minute0 + 2_000, 20.0, 10, 200),
                tick("600519", minute0 + 3_000, 10.5, 100, 1_050)
        ));

        assertEquals(2, bars.size());
        var bySym = bars.stream().collect(Collectors.toMap(Kline::getSymbol, k -> k));
        assertEquals(200.0, bySym.get("600519").getVolume());
        assertEquals(20.0, bySym.get("000001").getOpen());
        assertEquals(10, bySym.get("000001").getVolume());
    }
}
