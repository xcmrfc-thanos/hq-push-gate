package com.hqpush.biz.interfaces;

import com.hqpush.biz.application.DeveloperService;
import com.hqpush.biz.application.ApiResult;

import com.hqpush.biz.domain.DeveloperConfig;
import org.springframework.web.bind.annotation.*;

import java.util.Map;

/** 开发者接入配置（/api/v1/developer，JWT 用户面；docs/10 §7 契约 / docs/12 B18）。
 *  app_secret 明文仅在创建/轮换响应出现一次，其余查询一律脱敏。 */
@RestController
@RequestMapping("/api/v1/developer")
public class DeveloperController {

    private final DeveloperService devs;
    private final AuthController authHelper;

    public DeveloperController(DeveloperService devs, AuthController authHelper) {
        this.devs = devs;
        this.authHelper = authHelper;
    }

    @GetMapping("/config")
    public Map<String, Object> get(@RequestHeader("Authorization") String authorization) {
        long uid = authHelper.currentUid(authorization);
        DeveloperConfig c = devs.getByUser(uid);
        if (c == null) {
            return ApiResult.err(40401, "developer config not found (POST to create)");
        }
        return ApiResult.ok(Map.of(
                "access_key", c.getAccessKey(),
                "app_secret", com.hqpush.biz.application.SecretBox.mask(c.getAppSecret()),
                "push_mode", c.getPushMode(),
                "hook_url", c.getHookUrl()));
    }

    @PostMapping("/config")
    public Map<String, Object> create(@RequestHeader("Authorization") String authorization,
                                      @RequestBody(required = false) Map<String, String> body) {
        long uid = authHelper.currentUid(authorization);
        body = body == null ? Map.of() : body;
        var issued = devs.create(uid, body.get("pushMode"), body.get("hookUrl"));
        return ApiResult.ok(Map.of(
                "access_key", issued.cfg().getAccessKey(),
                "app_secret", issued.plainSecret(), // 仅此一次下发
                "push_mode", issued.cfg().getPushMode()));
    }

    @PostMapping("/config/rotate")
    public Map<String, Object> rotate(@RequestHeader("Authorization") String authorization) {
        long uid = authHelper.currentUid(authorization);
        var issued = devs.rotate(uid);
        return ApiResult.ok(Map.of(
                "access_key", issued.cfg().getAccessKey(),
                "app_secret", issued.plainSecret()));
    }

    @PutMapping("/config/push")
    public Map<String, Object> updatePush(@RequestHeader("Authorization") String authorization,
                                          @RequestBody Map<String, String> body) {
        long uid = authHelper.currentUid(authorization);
        devs.updatePush(uid, body.get("pushMode"), body.get("hookUrl"));
        return ApiResult.ok();
    }
}
