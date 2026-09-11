// RuleConditions T0 规则纯函数匹配（docs/10 §11.2，可单测、无 Flink 状态依赖）。
package com.hqpush.flink.function;

import com.fasterxml.jackson.databind.JsonNode;
import com.hqpush.contract.hqv1.RuleMsg;
import com.hqpush.contract.hqv1.Tick;

/**
 * 结构化条件匹配（condition JSON 与 docs/04 RuleMsg.condition 一致）。
 *
 * T0 批扩展（docs/10 §11.2）：
 * - PCT_CHANGE 增加 direction（up/down/both，默认 both）与 limit（涨跌停预警，
 *   板块幅度按代码段推导：主板 10 / 创业板科创板 20 / 北交所 30，触发阈值默认 板块幅度-0.2）；
 * - INDICATOR/VOLUME：累计成交量（股）> value；
 * - INDICATOR 其余子类型（MA/新高/连涨等）属 T1 日频批量评估引擎，实时路径显式不支持。
 */
public final class RuleConditions {

    private RuleConditions() {}

    /** 匹配入口；condition 非法时返回 false（含错误日志由调用方输出）。 */
    public static boolean match(RuleMsg rule, JsonNode c, Tick tick) {
        switch (rule.getType()) {
            case PRICE_ABOVE:
                return tick.getLastPrice() > c.path("threshold").asDouble();
            case PRICE_BELOW:
                return tick.getLastPrice() < c.path("threshold").asDouble();
            case PRICE_RANGE: {
                double low = c.path("low").asDouble();
                double high = c.path("high").asDouble();
                return tick.getLastPrice() >= low && tick.getLastPrice() <= high;
            }
            case PCT_CHANGE:
                return matchPctChange(c, tick);
            case INDICATOR:
                return matchIndicator(c, tick);
            default:
                return false;
        }
    }

    /** 涨跌幅：direction（up/down/both）+ limit（涨跌停预警，阈值默认 板块幅度-0.2）。 */
    static boolean matchPctChange(JsonNode c, Tick tick) {
        if (tick.getPreClose() <= 0) {
            return false;
        }
        double pct = (tick.getLastPrice() - tick.getPreClose()) / tick.getPreClose() * 100;
        String direction = c.path("direction").asText("both");
        if (c.path("limit").asBoolean(false)) {
            double limitPct = boardLimitPct(tick.getSymbol());
            double th = c.path("threshold").asDouble(limitPct - 0.2);
            return c.path("direction").asText("up").equals("up") ? pct >= th : pct <= -th;
        }
        double th = c.path("threshold").asDouble();
        switch (direction) {
            case "up":
                return pct >= th;
            case "down":
                return pct <= -th;
            default:
                return Math.abs(pct) >= th;
        }
    }

    /** 涨跌停幅度按代码段：科创板/创业板 20%，北交所 30%，其余主板 10%。 */
    static double boardLimitPct(String symbol) {
        if (symbol.startsWith("688") || symbol.startsWith("689")) {
            return 20;
        }
        if (symbol.startsWith("300") || symbol.startsWith("301")) {
            return 20;
        }
        if (symbol.startsWith("43") || symbol.startsWith("83") || symbol.startsWith("92")) {
            return 30;
        }
        return 10;
    }

    /** INDICATOR 实时子集：仅 VOLUME（累计成交量股 > value）；其余属 T1 日频批量评估。 */
    static boolean matchIndicator(JsonNode c, Tick tick) {
        return c.path("indicator").asText("").equals("VOLUME")
                && tick.getVolume() > c.path("value").asDouble();
    }

    /** 窗口脉冲：窗口期满时按方向比较 (last-ref)/ref*100 与阈值。 */
    public static boolean spikeTriggered(
            String direction, double thresholdPct, double refPrice, double last) {
        if (refPrice <= 0) {
            return false;
        }
        double pct = (last - refPrice) / refPrice * 100;
        switch (direction) {
            case "up":
                return pct >= thresholdPct;
            case "down":
                return pct <= -thresholdPct;
            default:
                return Math.abs(pct) >= thresholdPct;
        }
    }
}
