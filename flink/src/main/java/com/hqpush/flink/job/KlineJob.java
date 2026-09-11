package com.hqpush.flink.job;

import com.hqpush.contract.hqv1.Kline;
import com.hqpush.contract.hqv1.Tick;
import com.hqpush.flink.function.KlineAggregate;
import com.hqpush.flink.serde.ProtoDeserializer;
import com.hqpush.flink.serde.ProtoSerializer;
import org.apache.flink.api.common.eventtime.WatermarkStrategy;
import org.apache.flink.api.common.serialization.SerializationSchema;
import org.apache.flink.connector.base.DeliveryGuarantee;
import org.apache.flink.streaming.api.functions.windowing.ProcessWindowFunction;
import org.apache.flink.util.Collector;
import org.apache.flink.connector.kafka.sink.KafkaRecordSerializationSchema;
import org.apache.flink.connector.kafka.sink.KafkaSink;
import org.apache.flink.connector.kafka.source.KafkaSource;
import org.apache.flink.connector.kafka.source.enumerator.initializer.OffsetsInitializer;
import org.apache.flink.connector.kafka.source.reader.deserializer.KafkaRecordDeserializationSchema;
import org.apache.flink.streaming.api.datastream.DataStream;
import org.apache.flink.streaming.api.environment.StreamExecutionEnvironment;
import org.apache.flink.streaming.api.windowing.assigners.TumblingEventTimeWindows;
import org.apache.flink.streaming.api.windowing.time.Time;

import java.time.Duration;

/**
 * K线作业：tick_raw -> 1m 窗口 OHLCV -> snapshot_kline（docs/04 §1/§2 冻结契约）。
 * 消费侧：ingest-worker 归档 ClickHouse kline_local；quote-push（后续批次）写 kline:cur 与 WS 推送。
 */
public class KlineJob {

    public static void main(String[] args) throws Exception {
        String bootstrap = env("KAFKA_BROKERS", "kafka:9092");

        final StreamExecutionEnvironment env = StreamExecutionEnvironment.getExecutionEnvironment();
        env.enableCheckpointing(60_000);
        env.getConfig().setAutoWatermarkInterval(1000); // 周期 watermark 发射（B21 修复：fromSource 策略未被应用，须流级挂载）

        KafkaSource<Tick> tickSource = KafkaSource.<Tick>builder()
                .setBootstrapServers(bootstrap)
                .setTopics("tick_raw")
                .setGroupId("flink-kline")
                .setStartingOffsets(OffsetsInitializer.latest())
                .setDeserializer(KafkaRecordDeserializationSchema.valueOnly(
                        new ProtoDeserializer<>(Tick.parser(), Tick.class)))
                .build();

        KlineAggregate aggregate = new KlineAggregate();
        DataStream<Kline> klines = buildKlines(
                env.fromSource(tickSource, WatermarkStrategy.noWatermarks(), "tick_raw")
                        // B21 修复：策略经 fromSource 传入不生效（watermark 恒 Long.MIN_VALUE，窗口不闭合）；
                        // 改为流级 assignTimestampsAndWatermarks 挂载 + withIdleness（并行度>分区数时空闲源 subtask 不阻塞 watermark）
                        .assignTimestampsAndWatermarks(
                                WatermarkStrategy.<Tick>forBoundedOutOfOrderness(Duration.ofSeconds(5))
                                        .withIdleness(Duration.ofSeconds(10))
                                        .withTimestampAssigner((t, ts) -> t.getTimestampMs())))
                .name("kline-1m");

        KafkaSink<Kline> klineSink = KafkaSink.<Kline>builder()
                .setBootstrapServers(bootstrap)
                .setRecordSerializer(KafkaRecordSerializationSchema.builder()
                        .setTopic("snapshot_kline")
                        .setKeySerializationSchema((SerializationSchema<Kline>)
                                k -> (k.getMarket() + ":" + k.getSymbol()).getBytes())
                        .setValueSerializationSchema(new ProtoSerializer<>())
                        .build())
                .setDeliveryGuarantee(DeliveryGuarantee.AT_LEAST_ONCE)
                .build();

        klines.sinkTo(klineSink).name("snapshot_kline-sink");
        env.execute("kline-job");
    }

    /**
     * 1m K线聚合段（Kafka 拓扑与本地 MiniCluster 测试共用，docs/04 §1 Kline）：
     * 按 market:symbol 分组 → 1m 事件时间窗口 → OHLCV 聚合 → begin_ts=窗口起点。
     */
    public static org.apache.flink.streaming.api.datastream.SingleOutputStreamOperator<Kline> buildKlines(
            DataStream<Tick> ticks) {
        return ticks
                .keyBy(t -> t.getMarket() + ":" + t.getSymbol())
                .window(TumblingEventTimeWindows.of(Time.minutes(KlineAggregate.PERIOD_MIN)))
                .aggregate(new KlineAggregate(),
                        new ProcessWindowFunction<Kline, Kline, String, org.apache.flink.streaming.api.windowing.windows.TimeWindow>() {
                            @Override
                            public void process(String key, Context ctx,
                                                Iterable<Kline> in, Collector<Kline> out) {
                                Kline acc = in.iterator().next();
                                out.collect(acc.toBuilder().setBeginTsMs(ctx.window().getStart()).build());
                            }
                        });
    }

    private static String env(String key, String def) {
        String v = System.getenv(key);
        return v == null || v.isBlank() ? def : v;
    }
}
