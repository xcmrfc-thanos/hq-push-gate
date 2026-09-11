package com.hqpush.biz.interfaces;

import com.hqpush.biz.application.AdminService;
import com.hqpush.biz.application.ApiResult;

import org.springframework.beans.factory.annotation.Value;
import org.springframework.web.bind.annotation.*;

import java.util.List;
import java.util.Map;

/** 压测管理与链路观测（/api/v1/admin/*，docs/04 §6；内部令牌 + 边缘 JWT 双重防护）。 */
@RestController
@RequestMapping("/api/v1/admin")
public class AdminController {

    private final AdminService admin;
    private final boolean benchEnabled;

    public AdminController(AdminService admin,
                           @Value("${hq.bench.enabled:false}") boolean benchEnabled) {
        this.admin = admin;
        this.benchEnabled = benchEnabled;
    }

    private String guard(String token) {
        if (!admin.tokenOk(token)) {
            throw new IllegalArgumentException("unauthorized");
        }
        return null;
    }

    /** 模拟源调压（代理 hq-gateway 内部源；docs/04 §6 /admin/bench/tick-source）。 */
    @PostMapping("/bench/tick-source")
    public Map<String, Object> tickSource(@RequestHeader(value = "X-Internal-Token", required = false) String token,
                                          @RequestBody Map<String, Double> body) {
        guard(token);
        if (!benchEnabled) {
            return ApiResult.err(40301, "bench disabled");
        }
        return admin.tickSource(body.get("rate"));
    }

    /** 批量造压测用户。 */
    @PostMapping("/bench/users")
    public Map<String, Object> users(@RequestHeader(value = "X-Internal-Token", required = false) String token,
                                     @RequestBody Map<String, Integer> body) {
        guard(token);
        if (!benchEnabled) {
            return ApiResult.err(40301, "bench disabled");
        }
        int count = Math.min(Math.max(body.getOrDefault("count", 100), 1), 10000);
        List<Long> ids = admin.createUsers(count);
        return ApiResult.ok(Map.of("created", ids.size(), "ids", ids));
    }

    /** 批量造预警规则。 */
    @PostMapping("/bench/rules")
    public Map<String, Object> rules(@RequestHeader(value = "X-Internal-Token", required = false) String token,
                                     @RequestBody Map<String, Object> body) {
        guard(token);
        if (!benchEnabled) {
            return ApiResult.err(40301, "bench disabled");
        }
        long userId = ((Number) body.get("userId")).longValue();
        int count = Math.min(Math.max((int) (Number) body.getOrDefault("count", 100), 1), 100000);
        return ApiResult.ok(
                Map.of("created", admin.createRules(userId, count)));
    }

    /** 链路观测汇总。 */
    @GetMapping("/metrics/overview")
    public Map<String, Object> overview(@RequestHeader(value = "X-Internal-Token", required = false) String token)
            throws Exception {
        guard(token);
        return ApiResult.ok(admin.metricsOverview());
    }
}
