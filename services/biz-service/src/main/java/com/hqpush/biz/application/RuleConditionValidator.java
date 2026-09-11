// 规则条件 JSON 校验器（ADR-042 schema v2）。
// 规则侧扁平：type 由 RuleMsg.ruleType 承载，condition JSON 不含 type；组合（all/any）仅查询侧，
// 规则侧显式拒绝。单一事实源 = tests/fixtures/conditions.v2.json（JUnit 与 pytest 消费同一文件）。
// 职责：validate + normalize（PCT_CHANGE 负幅值归一为 direction=down，与 Flink matchPctChange
// 幅值+方向语义对齐），返回 canonical JSON 供入库与 outbox 下发。
package com.hqpush.biz.application;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ObjectNode;

public final class RuleConditionValidator {

    private static final ObjectMapper OM = new ObjectMapper();
    private static final double MAX_PRICE = 1_000_000.0;
    private static final double MAX_PCT = 100.0;
    private static final double MAX_VOLUME = 1e12;

    private RuleConditionValidator() {}

    /** 校验并规范化；非法抛 IllegalArgumentException（RuleService 转 400）。返回 canonical JSON。 */
    public static String canonicalize(String ruleType, String conditionJson) {
        if (ruleType == null || ruleType.isBlank()) {
            fail("ruleType required");
        }
        JsonNode c = parse(conditionJson);
        if (!c.isObject()) {
            fail("condition must be a JSON object");
        }
        if (c.has("type")) {
            fail("condition must not carry 'type' (RuleMsg.ruleType is authoritative)");
        }
        if (c.has("all") || c.has("any")) {
            fail("groups ('all'/'any') are not allowed in rule conditions");
        }
        ObjectNode out = switch (ruleType) {
            case "PRICE_ABOVE", "PRICE_BELOW" -> positive(c, "threshold", MAX_PRICE);
            case "PRICE_RANGE" -> range(c);
            case "PCT_CHANGE" -> pct(c);
            case "INDICATOR" -> indicator(c);
            default -> fail("unknown ruleType: " + ruleType);
        };
        overlay(c, out);
        return out.toString();
    }

    private static JsonNode parse(String conditionJson) {
        try {
            JsonNode n = OM.readTree(conditionJson == null ? "" : conditionJson);
            if (n == null || n.isMissingNode()) {
                fail("condition required");
            }
            return n;
        } catch (IllegalArgumentException e) {
            throw e;
        } catch (Exception e) {
            fail("condition is not valid JSON");
            return null; // unreachable
        }
    }

    private static ObjectNode positive(JsonNode c, String field, double max) {
        JsonNode v = c.get(field);
        if (v == null || !v.isNumber() || Double.isNaN(v.asDouble())
                || v.asDouble() <= 0 || v.asDouble() > max) {
            fail(field + " must be a number in (0, " + max + "]");
        }
        ObjectNode out = OM.createObjectNode();
        out.put(field, v.asDouble());
        return out;
    }

    private static ObjectNode range(JsonNode c) {
        JsonNode lo = c.get("low");
        JsonNode hi = c.get("high");
        if (lo == null || hi == null || !lo.isNumber() || !hi.isNumber()
                || lo.asDouble() <= 0 || hi.asDouble() > MAX_PRICE || lo.asDouble() >= hi.asDouble()) {
            fail("range must satisfy 0 < low < high <= " + MAX_PRICE);
        }
        ObjectNode out = OM.createObjectNode();
        out.put("low", lo.asDouble());
        out.put("high", hi.asDouble());
        return out;
    }

    private static ObjectNode pct(JsonNode c) {
        JsonNode t = c.get("threshold");
        if (t == null || !t.isNumber() || Double.isNaN(t.asDouble())
                || t.asDouble() == 0 || Math.abs(t.asDouble()) > MAX_PCT) {
            fail("threshold must be non-zero with |threshold| <= " + (long) MAX_PCT);
        }
        double v = t.asDouble();
        String direction = textOrNull(c, "direction", "up", "down", "both");
        // 负值 ≡ 跌幅别名：归一化为正幅值 + direction=down（与 Flink 幅值+方向语义对齐）
        if (v < 0) {
            if ("up".equals(direction)) {
                fail("negative threshold conflicts with direction='up'");
            }
            direction = "down";
            v = -v;
        }
        JsonNode limit = c.get("limit");
        if (limit != null && !limit.isBoolean()) {
            fail("limit must be boolean");
        }
        if (limit != null && limit.asBoolean() && !"up".equals(direction) && !"down".equals(direction)) {
            fail("limit=true requires explicit direction 'up' or 'down'");
        }
        ObjectNode out = OM.createObjectNode();
        out.put("threshold", v);
        if (direction != null) {
            out.put("direction", direction);
        }
        if (limit != null && limit.asBoolean()) {
            out.put("limit", true);
        }
        return out;
    }

    private static ObjectNode indicator(JsonNode c) {
        // T0 实时子集：仅 INDICATOR/VOLUME（累计成交量股 > value）；其余子类型属 T1 日频评估，不入实时规则
        if (!"VOLUME".equals(c.path("indicator").asText(""))) {
            fail("only INDICATOR/VOLUME is supported in realtime rules (T1 pending)");
        }
        if (!">".equals(c.path("op").asText(""))) {
            fail("only op '>' is supported");
        }
        JsonNode v = c.get("value");
        if (v == null || !v.isNumber() || Double.isNaN(v.asDouble())
                || v.asDouble() <= 0 || v.asDouble() > MAX_VOLUME) {
            fail("value must be a number in (0, " + MAX_VOLUME + "]");
        }
        ObjectNode out = OM.createObjectNode();
        out.put("indicator", "VOLUME");
        out.put("op", ">");
        out.put("value", v.asDouble());
        return out;
    }

    /** 公共叠加字段（docs/10 §11.0）：once/session/expire_at，可选；Flink 实时路径当前未强制。 */
    private static void overlay(JsonNode c, ObjectNode out) {
        JsonNode once = c.get("once");
        if (once != null) {
            if (!once.isBoolean()) {
                fail("once must be boolean");
            }
            if (once.asBoolean()) {
                out.put("once", true);
            }
        }
        String session = textOrNull(c, "session", "open", "close", "all");
        if (session != null) {
            out.put("session", session);
        }
        JsonNode exp = c.get("expire_at");
        if (exp != null) {
            if (!exp.isIntegralNumber() || exp.asLong() < 1) {
                fail("expire_at must be epoch millis >= 1");
            }
            out.put("expire_at", exp.asLong());
        }
    }

    private static String textOrNull(JsonNode c, String field, String... allowed) {
        JsonNode v = c.get(field);
        if (v == null) {
            return null;
        }
        String s = v.asText();
        for (String a : allowed) {
            if (a.equals(s)) {
                return s;
            }
        }
        fail(field + " must be one of: " + String.join("/", allowed));
        return null; // unreachable
    }

    private static <T> T fail(String msg) {
        throw new IllegalArgumentException(msg);
    }
}
