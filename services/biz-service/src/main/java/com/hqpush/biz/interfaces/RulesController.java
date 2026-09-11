package com.hqpush.biz.interfaces;

import com.hqpush.biz.application.AuthService;
import com.hqpush.biz.application.ApiResult;

import com.hqpush.biz.application.RuleService;
import com.hqpush.biz.domain.AlertRule;
import org.springframework.http.HttpStatus;
import org.springframework.web.bind.annotation.*;

import java.util.List;
import java.util.Map;

/** 预警规则 CRUD（/api/v1/rules，变更触发 outbox -> rule_bcast）。 */
@RestController
@RequestMapping("/api/v1/rules")
public class RulesController {

    private final RuleService rules;
    private final AuthController authHelper;

    public RulesController(RuleService rules, AuthController authHelper) {
        this.rules = rules;
        this.authHelper = authHelper;
    }

    @GetMapping
    public Map<String, Object> list(@RequestHeader("Authorization") String authorization) {
        long uid = authHelper.currentUid(authorization);
        List<AlertRule> data = rules.list(uid);
        return ApiResult.ok(data);
    }

    @PostMapping
    public Map<String, Object> create(@RequestHeader("Authorization") String authorization,
                                      @RequestBody AlertRule rule) {
        long uid = authHelper.currentUid(authorization);
        return ApiResult.ok(rules.create(uid, rule));
    }

    @PutMapping("/{id}")
    public Map<String, Object> update(@RequestHeader("Authorization") String authorization,
                                      @PathVariable long id, @RequestBody AlertRule rule) {
        long uid = authHelper.currentUid(authorization);
        return ApiResult.ok(rules.update(uid, id, rule));
    }

    @PatchMapping("/{id}/status")
    public Map<String, Object> updateStatus(@RequestHeader("Authorization") String authorization,
                                            @PathVariable long id, @RequestBody Map<String, Integer> body) {
        long uid = authHelper.currentUid(authorization);
        return ApiResult.ok(
                rules.updateStatus(uid, id, body.getOrDefault("status", 1)));
    }

    @DeleteMapping("/{id}")
    public Map<String, Object> delete(@RequestHeader("Authorization") String authorization,
                                      @PathVariable long id) {
        long uid = authHelper.currentUid(authorization);
        rules.delete(uid, id);
        return ApiResult.ok(Map.of());
    }

    @ResponseStatus(HttpStatus.BAD_REQUEST)
    static class BadRequest extends RuntimeException {}
}
