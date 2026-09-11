package com.hqpush.flink.job;

import com.hqpush.contract.hqv1.Kline;
import com.hqpush.contract.hqv1.Tick;
import com.hqpush.flink.function.KlineAggregate;
import com.hqpush.flink.serde.ProtoDeserializer;
import org.apache.flink.api.common.eventtime.WatermarkStrategy;
import org.apache.flink.connector.kafka.source.KafkaSource;
import org.apache.flink.connector.kafka.source.enumerator.initializer.OffsetsInitializer;
import org.apache.flink.connector.kafka.source.reader.deserializer.KafkaRecordDeserializationSchema;
import org.apache.flink.streaming.api.datastream.DataStream;
import org.apache.flink.streaming.api.environment.StreamExecutionEnvironment;
import org.apache.flink.streaming.api.functions.windowing.ProcessWindowFunction;
import org.apache.flink.streaming.api.windowing.assigners.TumblingEventTimeWindows;
import org.apache.flink.streaming.api.windowing.time.Time;
import org.apache.flink.streaming.api.windowing.windows.TimeWindow;
import org.apache.flink.util.Collector;

import java.time.Duration;

/** 联调诊断：KlineJob 同款 source+窗口，print tick 时间戳与窗口闭合（白盒）。 */
public class DebugKlineJob {

    public static void main(String[] args) throws Exception {
        String bootstrap = System.getenv().getOrDefault("KAFKA_BROKERS", "kafka:9092");
        final StreamExecutionEnvironment env = StreamExecutionEnvironment.getExecutionEnvironment();
        env.setParallelism(1);
        env.getConfig().setAutoWatermarkInterval(1000); // 周期 watermark 发射（B21 修复，与 KlineJob 同款）

        KafkaSource<Tick> tickSource = KafkaSource.<Tick>builder()
                .setBootstrapServers(bootstrap)
                .setTopics("tick_raw")
                .setGroupId("debug-kline-" + System.currentTimeMillis())
                .setStartingOffsets(OffsetsInitializer.latest())
                .setDeserializer(KafkaRecordDeserializationSchema.valueOnly(
                        new ProtoDeserializer<>(Tick.parser(), Tick.class)))
                .build();

        DataStream<Tick> ticks = env.fromSource(tickSource,
                WatermarkStrategy.noWatermarks(),
                "tick_raw")
                .assignTimestampsAndWatermarks(
                        WatermarkStrategy.<Tick>forBoundedOutOfOrderness(Duration.ofSeconds(5))
                                .withIdleness(Duration.ofSeconds(10))
                                .withTimestampAssigner((t, ts) -> t.getTimestampMs()));

        DataStream<Tick> filtered = ticks.filter(t -> {
            if (t.getSymbol().equals("600000")) {
                System.out.println("[tick600k] ts=" + t.getTimestampMs()
                        + " last=" + t.getLastPrice());
            }
            return true;
        });

        DataStream<Kline> klines = filtered
                .keyBy(t -> t.getMarket() + ":" + t.getSymbol())
                .window(TumblingEventTimeWindows.of(Time.minutes(1)))
                .aggregate(new KlineAggregate(),
                        new ProcessWindowFunction<Kline, Kline, String, TimeWindow>() {
                            @Override
                            public void process(String key, Context ctx,
                                                Iterable<Kline> in, Collector<Kline> out) {
                                Kline acc = in.iterator().next();
                                Kline outK = acc.toBuilder()
                                        .setBeginTsMs(ctx.window().getStart()).build();
                                System.out.println("[KLINE-1M] " + key + " begin=" + ctx.window().getStart()
                                        + " o=" + outK.getOpen() + " h=" + outK.getHigh()
                                        + " c=" + outK.getClose() + " v=" + outK.getVolume());
                                out.collect(outK);
                            }
                        });
        klines.print(); // 真实 sink：无 sink 的窗口链不会被 Flink 纳入执行图（DebugReadJob 同款验证）

        env.execute("debug-kline");
    }
}
