package com.hqpush.flink.job;

import com.hqpush.contract.hqv1.RuleMsg;
import com.hqpush.contract.hqv1.Tick;
import com.hqpush.flink.serde.ProtoDeserializer;
import org.apache.flink.api.common.eventtime.WatermarkStrategy;
import org.apache.flink.connector.kafka.source.KafkaSource;
import org.apache.flink.connector.kafka.source.enumerator.initializer.OffsetsInitializer;
import org.apache.flink.connector.kafka.source.reader.deserializer.KafkaRecordDeserializationSchema;
import org.apache.flink.streaming.api.datastream.DataStream;
import org.apache.flink.streaming.api.environment.StreamExecutionEnvironment;

import java.time.Duration;

/** 联调诊断：TickRuleJob 的同款 source 配置下，验证 Flink 侧能否消费并反序列化 tick/rule。 */
public class DebugReadJob {

    public static void main(String[] args) throws Exception {
        String bootstrap = System.getenv().getOrDefault("KAFKA_BROKERS", "kafka:9092");
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
                WatermarkStrategy.noWatermarks(), "tick_raw").filter(t -> {
                    System.out.println("[tick] " + t.getMarket() + ":" + t.getSymbol()
                            + " last=" + t.getLastPrice() + " ts=" + t.getTimestampMs());
                    return true;
                });

        KafkaSource<RuleMsg> ruleSource = KafkaSource.<RuleMsg>builder()
                .setBootstrapServers(bootstrap)
                .setTopics("rule_bcast")
                .setGroupId("debug-rule-" + System.currentTimeMillis())
                .setStartingOffsets(OffsetsInitializer.earliest())
                .setDeserializer(KafkaRecordDeserializationSchema.valueOnly(
                        new ProtoDeserializer<>(RuleMsg.parser(), RuleMsg.class)))
                .build();
        DataStream<RuleMsg> rules = env.fromSource(ruleSource, WatermarkStrategy.noWatermarks(), "rule_bcast")
                .filter(r -> {
                    System.out.println("[rule] id=" + r.getRuleId() + " v=" + r.getVersion()
                            + " " + r.getMarket() + ":" + r.getSymbol() + " " + r.getType() + " " + r.getCondition());
                    return true;
                });

        ticks.print();
        rules.print();
        env.execute("debug-read");
    }
}
