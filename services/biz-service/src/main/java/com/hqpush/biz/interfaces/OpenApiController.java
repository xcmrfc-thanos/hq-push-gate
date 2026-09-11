package com.hqpush.biz.interfaces;

import com.hqpush.biz.application.OpenApiException;
import com.hqpush.biz.application.ApiResult;

import com.hqpush.biz.application.OpenApiVerifier;
import com.hqpush.biz.application.RuleService;
import com.hqpush.biz.domain.AlertRuleMapper;
import com.hqpush.biz.domain.DeveloperConfig;
import jakarta.servlet.http.HttpServletRequest;
import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.*;

import java.security.SecureRandom;
import java.time.Duration;
import java.util.HexFormat;
import java.util.Map;

/** 开放面（/open/v1，AK/SK 签名鉴权，docs/11 §5；Branch 18）。
 *  ① ws-ticket：60s 一次性票据换 WS 连接（避免 AK 进 URL/日志）
 *  ② developer/config：开发者自助查询（脱敏）
 *  ③ alarm/subscriptions：API 订阅列表
 *  所有请求走 verify（ts/nonce/sign）+ usage 计量 + per-AK 限流。 */
@RestController
@RequestMapping("/open/v1")
public class OpenApiController {

    private static final SecureRandom RNG = new SecureRandom();

    private final OpenApiVerifier verifier;
    private final AlertRuleMapper alertRules;
    private final com.hqpush.biz.application.SubscribeService subscribeService;
    private final org.springframework.data.redis.core.StringRedisTemplate redis;

    public OpenApiController(OpenApiVerifier verifier, AlertRuleMapper alertRules,
                             com.hqpush.biz.application.SubscribeService subscribeService,
                             org.springframework.data.redis.core.StringRedisTemplate redis) {
        this.verifier = verifier;
        this.alertRules = alertRules;
        this.subscribeService = subscribeService;
        this.redis = redis;
    }

    /** WS 一次性票据：60s 有效、用后即焚（docs/11 §5.2）。 */
    @PostMapping("/ws-ticket")
    public ResponseEntity<Map<String, Object>> wsTicket(HttpServletRequest http) {
        return wrap(http, (dev, body) -> {
            String ticket = "tk_" + HexFormat.of().formatHex(random16());
            redis.opsForValue().set("open:ws:" + ticket, String.valueOf(dev.getUserId()), Duration.ofSeconds(60));
            return Map.of("ticket", ticket, "expires_in", 60,
                    "ws", "/ws?ticket=<ticket>");
        });
    }

    @GetMapping("/developer/config")
    public ResponseEntity<Map<String, Object>> config(HttpServletRequest http) {
        return wrap(http, (dev, body) -> Map.of(
                "access_key", dev.getAccessKey(),
                "app_secret", com.hqpush.biz.application.SecretBox.mask(dev.getAppSecret()),
                "push_mode", dev.getPushMode(),
                "hook_url", dev.getHookUrl()));
    }

    @GetMapping("/alarm/subscriptions")
    public ResponseEntity<Map<String, Object>> subscriptions(HttpServletRequest http) {
        return wrap(http, (dev, body) -> alertRules.listActiveBySource(dev.getUserId(), "api"));
    }

    /** 订阅创建（AK/SK 签名；docs/10 §7，Step1 D10-2 补实现）。 */
    @PostMapping("/alarm/subscribe")
    public ResponseEntity<Map<String, Object>> subscribe(HttpServletRequest http) {
        return wrap(http, (dev, body) -> {
            var req = parseJson(body);
            String stockCode = req.path("stockCode").asText();
            Double rise = req.path("riseThreshold").isNumber() ? req.path("riseThreshold").asDouble() : null;
            Double fall = req.path("fallThreshold").isNumber() ? req.path("fallThreshold").asDouble() : null;
            String prefer = req.path("channelPrefer").asText(null);
            var rule = subscribeService.subscribe(dev.getUserId(), stockCode, rise, fall, prefer);
            return Map.of("subscribe_id", rule.getId(), "subscribe_key", rule.getSubscribeKey(),
                    "symbol", rule.getSymbol());
        });
    }

    /** 订阅退订（AK/SK 签名）。 */
    @DeleteMapping("/alarm/subscribe")
    public ResponseEntity<Map<String, Object>> unsubscribe(HttpServletRequest http,
            @RequestParam("subscribeKey") String subscribeKey) {
        return wrap(http, (dev, body) -> {
            subscribeService.unsubscribeByKey(dev.getUserId(), subscribeKey);
            return Map.of("unsubscribed", true);
        });
    }

    private static com.fasterxml.jackson.databind.JsonNode parseJson(String body) {
        try {
            return new com.fasterxml.jackson.databind.ObjectMapper().readTree(body == null ? "{}" : body);
        } catch (Exception e) {
            throw new IllegalArgumentException("invalid json body");
        }
    }

    // ---- 公共：验签 + 计量 + 错误信封 ----

    private interface Handler {
        Object handle(DeveloperConfig dev, String body);
    }

    private ResponseEntity<Map<String, Object>> wrap(HttpServletRequest http, Handler h) {
        long t0 = System.nanoTime();
        String ak = http.getHeader("X-Access-Key");
        try {
            DeveloperConfig dev = verifier.verify(
                    http.getMethod(), http.getRequestURI(), http.getQueryString(), readBody(http),
                    ak, http.getHeader("X-Timestamp"), http.getHeader("X-Nonce"), http.getHeader("X-Signature"));
            Object data = h.handle(dev, readBody(http));
            record(ak, http, 200, t0);
            return ResponseEntity.ok(ApiResult.ok(data));
        } catch (OpenApiException e) {
            record(ak, http, e.getHttpStatus(), t0);
            ResponseEntity<Map<String, Object>> resp = ResponseEntity.status(e.getHttpStatus())
                    .body(ApiResult.err(e.getCode(), e.getMessage()));
            if (e.getRetryAfter() > 0) { // docs/11 §3：429 必带 Retry-After
                resp.getHeaders().add("Retry-After", String.valueOf(e.getRetryAfter()));
            }
            return resp;
        } catch (RuleService.NotFoundException e) {
            record(ak, http, 404, t0);
            return ResponseEntity.status(HttpStatus.NOT_FOUND).body(ApiResult.err(40401, e.getMessage()));
        }
    }

    private void record(String ak, HttpServletRequest http, int status, long t0) {
        verifier.recordUsage(ak == null ? "-" : ak, http.getRequestURI(), status, (System.nanoTime() - t0) / 1_000_000);
    }

    private static String readBody(HttpServletRequest http) {
        try {
            return new String(http.getInputStream().readAllBytes(), java.nio.charset.StandardCharsets.UTF_8);
        } catch (Exception e) {
            return "";
        }
    }

    private static byte[] random16() {
        byte[] b = new byte[16];
        RNG.nextBytes(b);
        return b;
    }
}
