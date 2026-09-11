package com.hqpush.biz.application;

import org.springframework.beans.factory.annotation.Qualifier;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.stereotype.Service;
import org.springframework.web.client.RestClient;

import javax.sql.DataSource;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.UUID;

/**
 * 压测管理（/api/v1/admin/bench/* 与 /admin/metrics/overview，docs/04 §6）。
 * 鉴权：APISIX 边缘 JWT 之外再校验 X-Internal-Token（与 ws-gateway/notify 同一口径）。
 * SQL 全参数绑定（batchUpdate %s 占位）。
 */
@Service
public class AdminService {

    private final RestClient gateway;
    private final String internalToken;
    private final JdbcTemplate jdbc;
    private final String ckUrl;
    private final String ckUser;
    private final String ckPassword;

    public AdminService(@Qualifier("gatewayRestClient") RestClient gateway,
                        @Value("${hq.internal.token}") String internalToken,
                        DataSource dataSource,
                        @Value("${hq.clickhouse.url}") String ckUrl,
                        @Value("${hq.clickhouse.user}") String ckUser,
                        @Value("${hq.clickhouse.password}") String ckPassword) {
        this.gateway = gateway;
        this.internalToken = internalToken;
        this.jdbc = new JdbcTemplate(dataSource);
        this.ckUrl = ckUrl;
        this.ckUser = ckUser;
        this.ckPassword = ckPassword;
    }

    /** 校验内部令牌（恒时比较）。 */
    public boolean tokenOk(String token) {
        return java.security.MessageDigest.isEqual(
                internalToken.getBytes(), (token == null ? "" : token).getBytes());
    }

    /** 模拟源调压：代理 hq-gateway /internal/bench/tick-source。 */
    @SuppressWarnings("unchecked")
    public Map<String, Object> tickSource(Double rate) {
        return gateway.post().uri("/internal/bench/tick-source")
                .header("X-Internal-Token", internalToken)
                .body(Map.of("rate", rate == null ? 0 : rate))
                .retrieve()
                .body(Map.class);
    }

    /** 批量造压测用户：同一随机口令（盐化 SHA-256 一次），用户名 bench_<批id>_<i>；id 沿用 maxId 顺延。 */
    public List<Long> createUsers(int count) {
        String password = "Bx-" + UUID.randomUUID();
        String hash = BCryptHash.of(password);
        String batchId = Long.toHexString(System.currentTimeMillis());
        long id = jdbc.queryForObject("SELECT IFNULL(MAX(id),0) FROM `user`", Long.class);
        List<Object[]> rows = new ArrayList<>(count);
        for (int i = 0; i < count; i++) {
            rows.add(new Object[]{++id, "bench_" + batchId + "_" + i, hash, "USER", 1});
        }
        jdbc.batchUpdate(
                "INSERT INTO user (id, username, password_hash, role, status) VALUES (?, ?, ?, ?, ?)",
                rows);
        List<Long> ids = new ArrayList<>(count);
        for (long v = id - count + 1; v <= id; v++) {
            ids.add(v);
        }
        return ids;
    }

    /** 批量造预警规则（price_above，阈值随机分布）；id 沿用 maxId 顺延。 */
    public int createRules(long userId, int count) {
        long id = jdbc.queryForObject("SELECT IFNULL(MAX(id),0) FROM alert_rule", Long.class);
        String cond = "{\"field\":\"last\",\"op\":\">\",\"value\":%f}";
        List<Object[]> rows = new ArrayList<>(count);
        for (int i = 0; i < count; i++) {
            double threshold = 10 + (i % 90) + (i % 100) / 100.0;
            rows.add(new Object[]{
                    ++id, userId, "A_SHARE", "600000", "price_above",
                    String.format(cond, threshold), 60, 1, 1L});
        }
        jdbc.batchUpdate(
                "INSERT INTO alert_rule (id, user_id, market, symbol, rule_type, `condition`,"
                        + " cooldown_sec, status, version) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
                rows);
        return count;
    }

    /** 链路观测汇总：CK 近 60s tick / 24h K线 + MySQL 用户与规则数。 */
    public Map<String, Object> metricsOverview() throws Exception {
        long ticks1m = ckCount("SELECT count() AS c FROM tick_local WHERE ts_ms >= {lo:Int64} FORMAT JSON",
                Map.of("lo", System.currentTimeMillis() - 60_000));
        long klines24h = ckCount(
                "SELECT count() AS c FROM kline_local WHERE begin_ts >= {lo:Int64} FORMAT JSON",
                Map.of("lo", System.currentTimeMillis() - 24 * 3600_000L));
        Long users = jdbc.queryForObject("SELECT COUNT(*) FROM user", Long.class);
        Long rules = jdbc.queryForObject("SELECT COUNT(*) FROM alert_rule WHERE status = 1", Long.class);
        return Map.of(
                "ticks_per_min", ticks1m,
                "klines_24h", klines24h,
                "users", users == null ? 0 : users,
                "rules_active", rules == null ? 0 : rules);
    }

    private long ckCount(String sql, Map<String, Object> params) {
        StringBuilder qs = new StringBuilder("query=").append(encode(sql));
        params.forEach((k, v) -> qs.append("&param_").append(k).append("=").append(v));
        String body;
        try {
            body = RestClient.create().get()
                    .uri(java.net.URI.create(ckUrl + "/?" + qs))
                    .headers(h -> h.setBasicAuth(ckUser, ckPassword))
                    .retrieve().body(String.class);
            var root = new com.fasterxml.jackson.databind.ObjectMapper().readTree(
                    body == null ? "[]" : body);
            return root.path("data").path(0).path("c").asLong(0);
        } catch (Exception e) {
            return 0;
        }
    }

    private String encode(String s) {
        return java.net.URLEncoder.encode(s, java.nio.charset.StandardCharsets.UTF_8);
    }
}
