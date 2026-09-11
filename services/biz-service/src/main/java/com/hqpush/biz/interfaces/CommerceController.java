package com.hqpush.biz.interfaces;

import com.hqpush.biz.application.AdminService;
import com.hqpush.biz.application.ApiResult;

import com.hqpush.biz.application.CommerceService;
import com.hqpush.biz.domain.UserVip;
import org.springframework.web.bind.annotation.*;

import java.util.Date;
import java.util.Map;

/** 商业化闭环（docs/12 B19）：管理端套餐发放 + 用户面自助绑定。
 *  /admin/commerce/* 在 APISIX 边缘 JWT 之外再验 X-Internal-Token（docs/11 §4），并写审计。 */
@RestController
public class CommerceController {

    private final CommerceService commerce;
    private final AdminService admin;
    private final AuthController authHelper;

    public CommerceController(CommerceService commerce, AdminService admin, AuthController authHelper) {
        this.commerce = commerce;
        this.admin = admin;
        this.authHelper = authHelper;
    }

    // ---- 管理端 ----

    @PostMapping("/api/v1/admin/commerce/grant")
    public Map<String, Object> grant(@RequestHeader(value = "X-Internal-Token", required = false) String token,
                                     @RequestBody Map<String, Object> body) {
        if (!admin.tokenOk(token)) {
            throw new IllegalArgumentException("unauthorized");
        }
        long userId = Long.parseLong(String.valueOf(body.get("userId")));
        String plan = String.valueOf(body.get("planType"));
        Date expire = body.get("expireTime") == null ? null
                : new Date(Long.parseLong(String.valueOf(body.get("expireTime"))));
        UserVip v = commerce.grant(userId, plan, expire, String.valueOf(body.getOrDefault("operator", "admin")));
        return ApiResult.ok(v);
    }

    // ---- 用户面自助绑定 ----

    @PutMapping("/api/v1/me/bindings/email")
    public Map<String, Object> bindEmail(@RequestHeader("Authorization") String authorization,
                                         @RequestBody Map<String, String> body) {
        long uid = authHelper.currentUid(authorization);
        commerce.bindEmail(uid, body.get("email"));
        return ApiResult.ok();
    }

    @PutMapping("/api/v1/me/bindings/phone")
    public Map<String, Object> bindPhone(@RequestHeader("Authorization") String authorization,
                                         @RequestBody Map<String, String> body) {
        long uid = authHelper.currentUid(authorization);
        commerce.bindPhone(uid, body.get("phone"));
        return ApiResult.ok();
    }

    @PutMapping("/api/v1/me/bindings/webhooks")
    public Map<String, Object> bindWebhooks(@RequestHeader("Authorization") String authorization,
                                            @RequestBody Map<String, String> body) {
        long uid = authHelper.currentUid(authorization);
        commerce.bindWebhooks(uid, body.get("dingtalk"), body.get("feishu"), body.get("channelDefault"));
        return ApiResult.ok();
    }

    @GetMapping("/api/v1/me/plan")
    public Map<String, Object> plan(@RequestHeader("Authorization") String authorization) {
        long uid = authHelper.currentUid(authorization);
        return ApiResult.ok(commerce.planOf(uid));
    }
}
