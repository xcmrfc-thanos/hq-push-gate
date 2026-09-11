// RuleConditions 单测：T0 规则纯函数（direction/limit/board/spike/volume）。
package com.hqpush.flink.function;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.hqpush.contract.hqv1.Market;
import com.hqpush.contract.hqv1.RuleMsg;
import com.hqpush.contract.hqv1.Tick;
import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.*;

class RuleConditionsTest {

    private static final ObjectMapper M = new ObjectMapper();

    private static RuleMsg rule(String type, String condition) throws Exception {
        return RuleMsg.newBuilder()
                .setRuleId(1).setVersion(1).setOp("UPSERT")
                .setMarket(Market.A_SHARE).setSymbol("600000")
                .setType(com.hqpush.contract.hqv1.RuleType.valueOf(type))
                .setCondition(condition).setCooldownSec(60)
                .build();
    }

    private static Tick tick(String symbol, double pre, double last, double volume) {
        return Tick.newBuilder()
                .setMarket(Market.A_SHARE).setSymbol(symbol).setExchange("SSE")
                .setTimestampMs(1788600000000L)
                .setLastPrice(last).setPreClose(pre).setVolume(volume)
                .build();
    }

    private static JsonNode cond(String json) throws Exception {
        return M.readTree(json);
    }

    @Test
    void priceAboveBelow() throws Exception {
        assertTrue(RuleConditions.match(rule("PRICE_ABOVE", "{\"threshold\":10}"),
                cond("{\"threshold\":10}"), tick("600000", 9, 10.5, 0)));
        assertFalse(RuleConditions.match(rule("PRICE_BELOW", "{\"threshold\":10}"),
                cond("{\"threshold\":10}"), tick("600000", 9, 10.5, 0)));
    }

    @Test
    void pctChangeDirection() throws Exception {
        // last=10.5, pre=10 → +5%
        Tick up = tick("600000", 10, 10.5, 0);
        Tick down = tick("600000", 10, 9.5, 0);
        assertTrue(RuleConditions.match(rule("PCT_CHANGE", "{\"threshold\":4,\"direction\":\"up\"}"),
                cond("{\"threshold\":4,\"direction\":\"up\"}"), up));
        assertFalse(RuleConditions.match(rule("PCT_CHANGE", "{\"threshold\":4,\"direction\":\"up\"}"),
                cond("{\"threshold\":4,\"direction\":\"up\"}"), down));
        assertTrue(RuleConditions.match(rule("PCT_CHANGE", "{\"threshold\":4,\"direction\":\"down\"}"),
                cond("{\"threshold\":4,\"direction\":\"down\"}"), down));
        // both：双向命中
        assertTrue(RuleConditions.match(rule("PCT_CHANGE", "{\"threshold\":4}"),
                cond("{\"threshold\":4}"), up));
        assertTrue(RuleConditions.match(rule("PCT_CHANGE", "{\"threshold\":4}"),
                cond("{\"threshold\":4}"), down));
    }

    @Test
    void limitBoardDerivation() throws Exception {
        // 创业板 300xxx → 板块幅度 20%，默认触发阈值 19.8
        RuleMsg cyb = rule("PCT_CHANGE", "{\"limit\":true,\"direction\":\"up\"}");
        assertTrue(RuleConditions.match(cyb, cond("{\"limit\":true,\"direction\":\"up\"}"),
                tick("300750", 10, 11.98, 0)));  // +19.8%
        assertFalse(RuleConditions.match(cyb, cond("{\"limit\":true,\"direction\":\"up\"}"),
                tick("300750", 10, 10.5, 0)));
        // 主板 600xxx → 10%，默认触发阈值 9.8
        RuleMsg main = rule("PCT_CHANGE", "{\"limit\":true,\"direction\":\"up\"}");
        assertTrue(RuleConditions.match(main, cond("{\"limit\":true,\"direction\":\"up\"}"),
                tick("600519", 10, 10.99, 0)));  // +9.9% ≥ 9.8
        assertFalse(RuleConditions.match(main, cond("{\"limit\":true,\"direction\":\"up\"}"),
                tick("600519", 10, 10.5, 0)));   // +5% < 9.8
    }

    @Test
    void volumeIndicator() throws Exception {
        assertTrue(RuleConditions.match(rule("INDICATOR", "{\"indicator\":\"VOLUME\",\"op\":\">\",\"value\":1000}"),
                cond("{\"indicator\":\"VOLUME\",\"op\":\">\",\"value\":1000}"), tick("600000", 10, 10.5, 2000)));
        assertFalse(RuleConditions.match(rule("INDICATOR", "{\"indicator\":\"VOLUME\",\"op\":\">\",\"value\":1000}"),
                cond("{\"indicator\":\"VOLUME\",\"op\":\">\",\"value\":1000}"), tick("600000", 10, 10.5, 500)));
        // 非 VOLUME 指标实时路径不支持
        assertFalse(RuleConditions.match(rule("INDICATOR", "{\"indicator\":\"RSI\",\"op\":\">\",\"value\":70}"),
                cond("{\"indicator\":\"RSI\",\"op\":\">\",\"value\":70}"), tick("600000", 10, 10.5, 5000)));
    }

    @Test
    void spikeTriggeredDirections() {
        assertTrue(RuleConditions.spikeTriggered("both", 2, 10, 10.3));   // +3%
        assertTrue(RuleConditions.spikeTriggered("both", 2, 10, 9.7));    // -3%
        assertFalse(RuleConditions.spikeTriggered("up", 2, 10, 9.9));     // -1%
        assertTrue(RuleConditions.spikeTriggered("down", 2, 10, 9.7));    // -3%
        assertFalse(RuleConditions.spikeTriggered("any", 2, 0, 0));       // refPrice<=0
    }

    @Test
    void boardLimitPctMapping() {
        assertEquals(10, RuleConditions.boardLimitPct("600000"), 0.001);
        assertEquals(20, RuleConditions.boardLimitPct("300750"), 0.001);
        assertEquals(20, RuleConditions.boardLimitPct("688981"), 0.001);
        assertEquals(30, RuleConditions.boardLimitPct("830799"), 0.001);
    }
}
