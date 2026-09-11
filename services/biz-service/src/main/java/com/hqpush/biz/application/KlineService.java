package com.hqpush.biz.application;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.springframework.beans.factory.annotation.Qualifier;
import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.stereotype.Service;
import org.springframework.web.client.RestClient;

import java.time.Duration;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * 历史K线查询（GET /quote/kline，docs/04 §6：缓存→CK/Doris）。
 * 读模型：1m/5m 为基期（Flink 实时聚合 / Baostock 深历史回填，docs/09 §5.1 Branch 4）；
 * 15/30/60/1440 由 1m 查询时滚动聚合。CK HTTP 接口 + {name:Type} 参数占位（全参数绑定）；
 * 查询去重不依赖 FINAL（docs/07 §8）。
 */
@Service
public class KlineService {

    private static final long CACHE_TTL_SECONDS = 60;
    private static final int MAX_LIMIT = 1000;

    private final RestClient ck;
    private final StringRedisTemplate redis;
    private final ObjectMapper mapper = new ObjectMapper();

    public KlineService(@Qualifier("ckRestClient") RestClient ck, StringRedisTemplate redis) {
        this.ck = ck;
        this.redis = redis;
    }

    /** 单根K线：t=begin_ts(ms)，o/h/l/c，v=量（股/份），a=额。 */
    public List<Map<String, Object>> klines(String market, String symbol, int period,
                                            Long start, Long end, int limit) throws Exception {
        if (limit <= 0) {
            limit = 300;
        }
        limit = Math.min(limit, MAX_LIMIT);

        // 显式区间直查；最近 N 根（无 start）走短 TTL 缓存，吸收首屏重复请求
        final boolean latestMode = start == null;
        final String cacheKey = "kline:hist:%s:%s:%d:%d".formatted(market, symbol, period, limit);
        if (latestMode) {
            String cached = redis.opsForValue().get(cacheKey);
            if (cached != null) {
                return mapper.readValue(cached, mapper.getTypeFactory()
                        .constructCollectionType(List.class, LinkedHashMap.class));
            }
        }

        List<Map<String, Object>> bars = period <= 5
                ? queryBase(market, symbol, period, start, end, limit)
                : queryRollup(market, symbol, period, start, end, limit);

        if (latestMode && !bars.isEmpty()) {
            redis.opsForValue().set(cacheKey, mapper.writeValueAsString(bars),
                    Duration.ofSeconds(CACHE_TTL_SECONDS));
        }
        return bars;
    }

    /** CK 查询：SQL 文本静态、业务值经 {name:Type} 占位下发，结果为 FORMAT JSON。 */
    private JsonNode ckQuery(String sql, Map<String, Object> params) {
        StringBuilder qs = new StringBuilder("query=").append(encode(sql));
        params.forEach((k, v) -> qs.append("&param_").append(k)
                .append("=").append(encode(String.valueOf(v))));
        String body = ck.get()
                .uri(java.net.URI.create("/?" + qs))
                .retrieve().body(String.class);
        try {
            return mapper.readTree(body == null ? "[]" : body).path("data");
        } catch (Exception e) {
            throw new IllegalStateException("bad ck response", e);
        }
    }

    private String encode(String s) {
        return java.net.URLEncoder.encode(s, java.nio.charset.StandardCharsets.UTF_8);
    }

    /** 基期直查：ReplacingMergeTree 同 event_id 多版本按 ingest_version 取最新（docs/07 §8）。 */
    private List<Map<String, Object>> queryBase(String market, String symbol, int period,
                                                Long start, Long end, int limit) {
        boolean desc = start == null;
        String sql = """
                SELECT begin_ts, open, high, low, close, volume, amount FROM (
                  SELECT event_id, ingest_version, begin_ts, open, high, low, close, volume, amount,
                         row_number() OVER (PARTITION BY event_id ORDER BY ingest_version DESC) AS rn
                  FROM kline_local
                  WHERE market = {mkt:String} AND symbol = {sym:String} AND period_min = {per:UInt16}
                    AND begin_ts >= {lo:Int64} AND begin_ts < {hi:Int64}
                ) WHERE rn = 1
                ORDER BY begin_ts %s LIMIT {lim:UInt32} FORMAT JSON
                """.formatted(desc ? "DESC" : "ASC");
        JsonNode rows = ckQuery(sql, Map.of(
                "mkt", market, "sym", symbol, "per", period,
                "lo", start == null ? 0L : start, "hi", end == null ? Long.MAX_VALUE : end,
                "lim", limit));
        List<Map<String, Object>> out = new ArrayList<>();
        for (JsonNode r : rows) {
            out.add(bar(r.path("begin_ts").asLong(), r.path("open").asDouble(),
                    r.path("high").asDouble(), r.path("low").asDouble(),
                    r.path("close").asDouble(), r.path("volume").asDouble(),
                    r.path("amount").asDouble()));
        }
        if (desc) {
            java.util.Collections.reverse(out);
        }
        return out;
    }

    /** 高周期由 1m 滚动聚合（argMin/argMax 定首尾，min/max 定极值），桶长为参数。 */
    private List<Map<String, Object>> queryRollup(String market, String symbol, int period,
                                                  Long start, Long end, int limit) {
        long bucketMs = period * 60_000L;
        boolean desc = start == null;
        String sql = """
                SELECT bucket_ms,
                       argMin(open, begin_ts)  AS open,
                       max(high)               AS high,
                       min(low)                AS low,
                       argMax(close, begin_ts) AS close,
                       sum(volume)             AS volume,
                       sum(amount)             AS amount
                FROM (
                  SELECT event_id, ingest_version, begin_ts, open, high, low, close, volume, amount,
                         intDiv(begin_ts, {bkt:Int64}) * {bkt:Int64} AS bucket_ms,
                         row_number() OVER (PARTITION BY event_id ORDER BY ingest_version DESC) AS rn
                  FROM kline_local
                  WHERE market = {mkt:String} AND symbol = {sym:String} AND period_min = 1
                    AND begin_ts >= {lo:Int64} AND begin_ts < {hi:Int64}
                ) WHERE rn = 1
                GROUP BY bucket_ms
                ORDER BY bucket_ms %s LIMIT {lim:UInt32} FORMAT JSON
                """.formatted(desc ? "DESC" : "ASC");
        JsonNode rows = ckQuery(sql, Map.of(
                "bkt", bucketMs, "mkt", market, "sym", symbol,
                "lo", start == null ? 0L : start, "hi", end == null ? Long.MAX_VALUE : end,
                "lim", limit));
        List<Map<String, Object>> out = new ArrayList<>();
        for (JsonNode r : rows) {
            out.add(bar(r.path("bucket_ms").asLong(), r.path("open").asDouble(),
                    r.path("high").asDouble(), r.path("low").asDouble(),
                    r.path("close").asDouble(), r.path("volume").asDouble(),
                    r.path("amount").asDouble()));
        }
        if (desc) {
            java.util.Collections.reverse(out);
        }
        return out;
    }

    private Map<String, Object> bar(long t, double o, double h, double l, double c, double v, double a) {
        Map<String, Object> m = new LinkedHashMap<>();
        m.put("t", t);
        m.put("o", o);
        m.put("h", h);
        m.put("l", l);
        m.put("c", c);
        m.put("v", v);
        m.put("a", a);
        return m;
    }
}
