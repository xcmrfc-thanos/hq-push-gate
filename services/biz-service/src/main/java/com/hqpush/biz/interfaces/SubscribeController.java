package com.hqpush.biz.interfaces;

import com.hqpush.biz.application.SubscribeService;
import com.hqpush.biz.application.ApiResult;

import com.hqpush.biz.domain.AlertRule;
import org.springframework.web.bind.annotation.*;

import java.util.List;
import java.util.Map;

/**
 * 订阅糖衣接口（/api/v1/alarm/subscribe，docs/10 §3.2/§7：外部设计映射到 alert_rule）。
 * 配额（api_max_sub）与免费套餐短信拦截在 SubscribeService 后端强制。
 */
@RestController
@RequestMapping("/api/v1/alarm/subscribe")
public class SubscribeController {

    private final SubscribeService subscribe;
    private final AuthController authHelper;

    public SubscribeController(SubscribeService subscribe, AuthController authHelper) {
        this.subscribe = subscribe;
        this.authHelper = authHelper;
    }

    /** 创建订阅：rise/fall 阈值（小数比例，0.05=5%）+ 渠道偏好 → PCT_CHANGE 规则。 */
    @PostMapping
    public Map<String, Object> create(@RequestHeader("Authorization") String authorization,
                                      @RequestBody Map<String, Object> body) {
        long uid = authHelper.currentUid(authorization);
        String stockCode = (String) body.get("stockCode");
        Double rise = body.get("riseThreshold") == null ? null
                : ((Number) body.get("riseThreshold")).doubleValue();
        Double fall = body.get("fallThreshold") == null ? null
                : ((Number) body.get("fallThreshold")).doubleValue();
        String prefer = body.get("channelPrefer") == null ? null
                : body.get("channelPrefer").toString();
        subscribe.checkChannelPrefer(uid, prefer);
        subscribe.checkQuota(uid, "api");
        AlertRule r = subscribe.subscribe(uid, stockCode, rise, fall, prefer);
        return ApiResult.ok(Map.of(
                "subscribeId", r.getId(),
                "subscribeKey", r.getSubscribeKey(),
                "stockCode", r.getSymbol(),
                "channelPrefer", r.getChannelPrefer() == null ? "email" : r.getChannelPrefer()));
    }

    /** 退订：按 subscribeKey 停用该订阅。 */
    @DeleteMapping
    public Map<String, Object> delete(@RequestHeader("Authorization") String authorization,
                                      @RequestParam String subscribeKey) {
        long uid = authHelper.currentUid(authorization);
        subscribe.unsubscribeByKey(uid, subscribeKey);
        return ApiResult.ok();
    }

    /** 订阅列表（source=api）。 */
    @GetMapping
    public Map<String, Object> list(@RequestHeader("Authorization") String authorization) {
        long uid = authHelper.currentUid(authorization);
        return ApiResult.ok(subscribe.listApi(uid));
    }
}
