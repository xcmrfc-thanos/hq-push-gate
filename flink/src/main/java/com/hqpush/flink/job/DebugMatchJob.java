package com.hqpush.flink.job;

import com.hqpush.contract.hqv1.AlertEvent;
import com.hqpush.contract.hqv1.RuleMsg;
import com.hqpush.contract.hqv1.Tick;
import com.hqpush.flink.function.RuleMatchFunction;
import com.hqpush.flink.serde.ProtoDeserializer;
import org.apache.flink.api.common.eventtime.WatermarkStrategy;
import org.apache.flink.connector.kafka.source.KafkaSource;
import org.apache.flink.connector.kafka.source.enumerator.initializer.OffsetsInitializer;
import org.apache.flink.connector.kafka.source.reader.deserializer.KafkaRecordDeserializationSchema;
import org.apache.flink.streaming.api.datastream.DataStream;
import org.apache.flink.streaming.api.environment.StreamExecutionEnvironment;

import java.time.Duration;

/** 联调诊断（白盒）：同 TickRuleJob 的 source + broadcast 规则匹配，alert 直接 print（绕过 Kafka sink 黑盒）。 */
public class DebugMatchJob {

    public static void main(String[] args) throws Exception {
        String bootstrap = System.getenv().getOrDefault("KAFKA_BROKERS", "kafka:9092");
        String ruleGroup = System.getenv().getOrDefault("RULE_BCAST_GROUP", "debug-match-" + System.currentTimeMillis());
        final StreamExecutionEnvironment env = StreamExecutionEnvironment.getExecutionEnvironment();
        env.setParallelism(1);

        KafkaSource<Tick> tickSource = KafkaSource.<Tick>builder()
                .setBootstrapServers(bootstrap)
                .setTopics("tick_raw")
                .setGroupId("debug-tick-" + System.currentTimeMillis())
                .setStartingOffsets(OffsetsInitializer.latest())
                .setDeserializer(KafkaRecordDeserializationSchema.valueOnly(
                        new ProtoDeserializer<>(Tick.parser(), Tick.class)))
                .build();
        DataStream<Tick> ticks = env.fromSource(tickSource,
                WatermarkStrategy.<Tick>forBoundedOutOfOrderness(Duration.ofSeconds(5))
                        .withTimestampAssigner((t, ts) -> t.getTimestampMs()),
                "tick_raw");
        ticks.print("tick-in");
        ticks = ticks.keyBy(t -> t.getMarket() + ":" + t.getSymbol());

        KafkaSource<RuleMsg> ruleSource = KafkaSource.<RuleMsg>builder()
                .setBootstrapServers(bootstrap)
                .setTopics("rule_bcast")
                .setGroupId(ruleGroup)
                .setStartingOffsets(OffsetsInitializer.earliest())
                .setDeserializer(KafkaRecordDeserializationSchema.valueOnly(
                        new ProtoDeserializer<>(RuleMsg.parser(), RuleMsg.class)))
                .build();
        DataStream<RuleMsg> rules = env.fromSource(ruleSource, WatermarkStrategy.noWatermarks(), "rule_bcast");
        rules.print("rule-in");

        DataStream<AlertEvent> alerts = ticks
                .connect(rules.broadcast(RuleMatchFunction.RULES_DESCRIPTOR))
                .process(new RuleMatchFunction());
        alerts.print("alert-hit");

        env.execute("debug-match");
    }
}
