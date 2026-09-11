package com.hqpush.flink.job;

import com.hqpush.contract.hqv1.AlertEvent;
import com.hqpush.contract.hqv1.RuleMsg;
import com.hqpush.contract.hqv1.Tick;
import com.hqpush.flink.function.RuleMatchFunction;
import com.hqpush.flink.serde.ProtoDeserializer;
import com.hqpush.flink.serde.ProtoSerializer;
import org.apache.flink.api.common.eventtime.WatermarkStrategy;
import org.apache.flink.api.common.serialization.SerializationSchema;
import org.apache.flink.connector.base.DeliveryGuarantee;
import org.apache.flink.connector.kafka.sink.KafkaRecordSerializationSchema;
import org.apache.flink.connector.kafka.sink.KafkaSink;
import org.apache.flink.connector.kafka.source.KafkaSource;
import org.apache.flink.connector.kafka.source.enumerator.initializer.OffsetsInitializer;
import org.apache.flink.connector.kafka.source.reader.deserializer.KafkaRecordDeserializationSchema;
import org.apache.flink.streaming.api.datastream.DataStream;
import org.apache.flink.streaming.api.environment.StreamExecutionEnvironment;

import java.time.Duration;

/**
 * U0 规则作业主入口：tick_raw × rule_bcast -> alert_event。
 * Topic 与消息契约为冻结项（docs/04 §2）；Kafka 连接器版本与 Flink 1.20 兼容矩阵见 docs/09 §7.1。
 */
public class TickRuleJob {

    public static void main(String[] args) throws Exception {
        String bootstrap = env("KAFKA_BROKERS", "kafka:9092");
        // 消费组可覆盖：rule_bcast 用 earliest 重放建广播状态（重启丢状态，docs/10 §11.8）；
        // 无 savepoint 的本地/联调场景换新组名即强制全量重放规则。
        String ruleGroup = env("RULE_BCAST_GROUP", "flink-rules-bcast");

        final StreamExecutionEnvironment env = StreamExecutionEnvironment.getExecutionEnvironment();
        env.enableCheckpointing(60_000); // checkpoint/savepoint 是 U1 验收项，U0 即保留

        // tick_raw：组消费、事件时间语义
        KafkaSource<Tick> tickSource = KafkaSource.<Tick>builder()
                .setBootstrapServers(bootstrap)
                .setTopics("tick_raw")
                .setGroupId("flink-rules")
                .setStartingOffsets(OffsetsInitializer.latest())
                .setDeserializer(KafkaRecordDeserializationSchema.valueOnly(
                        new ProtoDeserializer<>(Tick.parser(), Tick.class)))
                .build();

        // rule_bcast：单分区广播流，从最早开始保证规则状态可重建
        KafkaSource<RuleMsg> ruleSource = KafkaSource.<RuleMsg>builder()
                .setBootstrapServers(bootstrap)
                .setTopics("rule_bcast")
                .setGroupId(ruleGroup)
                .setStartingOffsets(OffsetsInitializer.earliest())
                .setDeserializer(KafkaRecordDeserializationSchema.valueOnly(
                        new ProtoDeserializer<>(RuleMsg.parser(), RuleMsg.class)))
                .build();

        DataStream<Tick> ticks = env.fromSource(tickSource,
                        WatermarkStrategy.<Tick>forBoundedOutOfOrderness(Duration.ofSeconds(5))
                                .withTimestampAssigner((t, ts) -> t.getTimestampMs()),
                        "tick_raw")
                .keyBy(t -> t.getMarket() + ":" + t.getSymbol());

        DataStream<RuleMsg> rules = env.fromSource(ruleSource,
                WatermarkStrategy.noWatermarks(), "rule_bcast");

        DataStream<AlertEvent> alerts = ticks
                .connect(rules.broadcast(RuleMatchFunction.RULES_DESCRIPTOR))
                .process(new RuleMatchFunction())
                .name("rule-match");

        KafkaSink<AlertEvent> alertSink = KafkaSink.<AlertEvent>builder()
                .setBootstrapServers(bootstrap)
                .setRecordSerializer(KafkaRecordSerializationSchema.builder()
                        .setTopic("alert_event")
                        .setKeySerializationSchema((SerializationSchema<AlertEvent>)
                                e -> String.valueOf(e.getRuleId()).getBytes())
                        .setValueSerializationSchema(new ProtoSerializer<>())
                        .build())
                .setDeliveryGuarantee(DeliveryGuarantee.AT_LEAST_ONCE)
                .build();

        alerts.sinkTo(alertSink).name("alert_event-sink");
        env.execute("tick-rule-job");
    }

    private static String env(String key, String def) {
        String v = System.getenv(key);
        return v == null || v.isBlank() ? def : v;
    }
}
