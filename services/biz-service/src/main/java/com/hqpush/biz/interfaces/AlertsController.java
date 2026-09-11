package com.hqpush.biz.interfaces;

import com.hqpush.biz.application.AlertProxyService;
import com.hqpush.biz.application.ApiResult;

import org.springframework.web.bind.annotation.*;

import java.util.List;
import java.util.Map;

/** 告警补拉（GET /api/v1/alerts?cursor=&limit=）与 ACK（docs/04 §6）。 */
@RestController
@RequestMapping("/api/v1/alerts")
public class AlertsController {

    private final AlertProxyService alerts;
    private final AuthController authHelper;

    public AlertsController(AlertProxyService alerts, AuthController authHelper) {
        this.alerts = alerts;
        this.authHelper = authHelper;
    }

    @GetMapping
    public Map<String, Object> pull(@RequestHeader("Authorization") String authorization,
                                    @RequestParam(defaultValue = "0") long cursor,
                                    @RequestParam(defaultValue = "50") int limit) {
        long uid = authHelper.currentUid(authorization);
        return alerts.pull(uid, cursor, limit);
    }

    @PostMapping("/ack")
    public Map<String, Object> ack(@RequestHeader("Authorization") String authorization,
                                   @RequestBody Map<String, String> body) {
        long uid = authHelper.currentUid(authorization);
        boolean updated = alerts.ack(uid, body.get("delivery_id"));
        return ApiResult.ok(Map.of("updated", updated));
    }
}
