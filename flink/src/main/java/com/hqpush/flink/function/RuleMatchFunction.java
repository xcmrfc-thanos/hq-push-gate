package com.hqpush.flink.function;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.hqpush.contract.hqv1.AlertEvent;
import com.hqpush.contract.hqv1.RuleMsg;
import com.hqpush.contract.hqv1.Tick;
import org.apache.flink.api.common.state.MapState;
import org.apache.flink.api.common.state.MapStateDescriptor;
import org.apache.flink.api.common.state.ReadOnlyBroadcastState;
import org.apache.flink.configuration.Configuration;
import org.apache.flink.streaming.api.functions.co.KeyedBroadcastProcessFunction;
import org.apache.flink.util.Collector;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

/**
 * 价格规则匹配：tick（keyed by market:symbol）× rule_bcast（broadcast state）。
 *
 * 冻结约束（docs/07）：
 * - RuleMsg 不携带 user_ids，Flink 只判断命中，用户展开由 notify 完成；
 * - 规则版本按规则单调递增，旧版本被覆盖；
 * - 冷却状态（cool:{ruleId}）在本算子 MapState 中防抖；
 * - event_id = rule_id + rule_version + trigger_window（唯一事件键）。
 */
public class RuleMatchFunction extends KeyedBroadcastProcessFunction<
        String, Tick, RuleMsg, AlertEvent> {

    private static final Logger LOG = LoggerFactory.getLogger(RuleMatchFunction.class);

    public static final MapStateDescriptor<String, RuleMsg> RULES_DESCRIPTOR =
            new MapStateDescriptor<>("rules", String.class, RuleMsg.class);

    private static final ObjectMapper MAPPER = new ObjectMapper();

    /** 冷却状态：ruleId -> 上次触发时间（秒）。 */
    private transient MapState<Long, Long> lastTrigger;

    /** 脉冲窗口基准：ruleId -> 窗口起点价（双精度以位模式存 Long，见 spike 状态键）。 */
    private transient MapState<String, Double> spikeRefPrice;
    private transient MapState<String, Long> spikeRefTs;

    @Override
    public void open(Configuration parameters) {
        lastTrigger = getRuntimeContext().getMapState(
                new MapStateDescriptor<>("lastTrigger", Long.class, Long.class));
        spikeRefPrice = getRuntimeContext().getMapState(
                new MapStateDescriptor<>("spikeRefPrice", String.class, Double.class));
        spikeRefTs = getRuntimeContext().getMapState(
                new MapStateDescriptor<>("spikeRefTs", String.class, Long.class));
    }

    @Override
    public void processElement(Tick tick, ReadOnlyContext ctx, Collector<AlertEvent> out) throws Exception {
        ReadOnlyBroadcastState<String, RuleMsg> rules = ctx.getBroadcastState(RULES_DESCRIPTOR);
        // 遍历该标的命中的规则（U0 规模可线性扫描；U3 起按 symbol 二级索引优化）
        for (java.util.Map.Entry<String, RuleMsg> e : rules.immutableEntries()) {
            RuleMsg rule = e.getValue();
            if (rule.getOp().equals("DELETE")) {
                continue;
            }
            if (rule.getMarket() != tick.getMarket() || !rule.getSymbol().equals(tick.getSymbol())) {
                continue;
            }
            JsonNode c;
            try {
                c = MAPPER.readTree(rule.getCondition());
            } catch (Exception ex) {
                LOG.warn("bad rule condition, ruleId={}", rule.getRuleId(), ex);
                continue;
            }
            // 脉冲窗口规则（docs/10 §11.2 #7/#8）：窗口基准滚动，期满按方向比较
            if (rule.getType() == com.hqpush.contract.hqv1.RuleType.PCT_CHANGE
                    && c.path("window_sec").asInt(0) > 0) {
                String sk = rule.getRuleId() + "";
                Double refP = spikeRefPrice.get(sk);
                Long refT = spikeRefTs.get(sk);
                if (refP == null) {
                    spikeRefPrice.put(sk, tick.getLastPrice());
                    spikeRefTs.put(sk, tick.getTimestampMs());
                    continue;
                }
                long winMs = c.path("window_sec").asLong(60) * 1000;
                if (tick.getTimestampMs() - refT < winMs) {
                    continue; // 窗口未满
                }
                String dir = c.path("direction").asText("both");
                double th = c.path("threshold").asDouble();
                if (RuleConditions.spikeTriggered(dir, th, refP, tick.getLastPrice())) {
                    out.collect(AlertEvent.newBuilder()
                            .setEventId("r" + rule.getRuleId() + "-v" + rule.getVersion()
                                    + "-w" + tick.getTimestampMs() / 1000)
                            .setRuleId(rule.getRuleId())
                            .setVersion(rule.getVersion())
                            .setMarket(rule.getMarket())
                            .setSymbol(rule.getSymbol())
                            .setTitle(rule.getSymbol() + ("down".equals(dir) ? " 快速跳水" : " 快速拉升"))
                            .setDetailJson(String.format(
                                    "{\"rule_type\":\"PCT_CHANGE\",\"spike\":true,\"direction\":\"%s\","
                                            + "\"window_sec\":%d,\"window_pct\":%.4f,\"ref\":%.2f,\"last\":%.2f}",
                                    dir, c.path("window_sec").asInt(60),
                                    (tick.getLastPrice() - refP) / refP * 100, refP, tick.getLastPrice()))
                            .setTriggerTs(tick.getTimestampMs())
                            .build());
                }
                spikeRefPrice.put(sk, tick.getLastPrice());
                spikeRefTs.put(sk, tick.getTimestampMs());
                continue;
            }
            if (!matched(rule, c, tick)) {
                continue;
            }
            // 修复（B18 联调 P0）：无触发记录用 0L 起算；原 Long.MIN_VALUE 使 nowSec-last
            // long 溢出为负 → 负数 < cooldownSec 恒成立 → 所有规则被永久"冷却"，alert 从不触发
            long nowSec = tick.getTimestampMs() / 1000;
            long last = lastTrigger.contains(rule.getRuleId()) ? lastTrigger.get(rule.getRuleId()) : 0L;
            if (nowSec - last < rule.getCooldownSec()) {
                continue; // 冷却窗口内防抖
            }
            lastTrigger.put(rule.getRuleId(), nowSec);
            out.collect(AlertEvent.newBuilder()
                    .setEventId("r" + rule.getRuleId() + "-v" + rule.getVersion()
                            + "-w" + tick.getTimestampMs() / 1000)
                    .setRuleId(rule.getRuleId())
                    .setVersion(rule.getVersion())
                    .setMarket(rule.getMarket())
                    .setSymbol(rule.getSymbol())
                    .setTitle(rule.getSymbol() + " 价格预警")
                    .setDetailJson(detail(rule, tick))
                    .setTriggerTs(tick.getTimestampMs())
                    .build());
        }
    }

    @Override
    public void processBroadcastElement(RuleMsg rule, Context ctx, Collector<AlertEvent> out) throws Exception {
        org.apache.flink.api.common.state.BroadcastState<String, RuleMsg> rules =
                ctx.getBroadcastState(RULES_DESCRIPTOR);
        if (rule.getOp().equals("DELETE")) {
            rules.remove(String.valueOf(rule.getRuleId()));
        } else {
            // 版本单调递增校验：旧版本/同版本幂等丢弃
            RuleMsg cur = rules.get(String.valueOf(rule.getRuleId()));
            if (cur == null || rule.getVersion() > cur.getVersion()) {
                rules.put(String.valueOf(rule.getRuleId()), rule);
            }
        }
    }

    /** 结构化条件匹配（condition JSON 与 docs/04 RuleMsg.condition 一致）；委托纯函数便于单测。 */
    private boolean matched(RuleMsg rule, JsonNode c, Tick tick) {
        try {
            return RuleConditions.match(rule, c, tick);
        } catch (Exception ex) {
            LOG.warn("bad rule condition, ruleId={}", rule.getRuleId(), ex);
            return false;
        }
    }

    private String detail(RuleMsg rule, Tick tick) {
        return String.format(
                "{\"rule_type\":\"%s\",\"last\":%f,\"pre_close\":%f,\"ts\":%d}",
                rule.getType(), tick.getLastPrice(), tick.getPreClose(), tick.getTimestampMs());
    }
}
