package com.hqpush.biz.application;

import com.hqpush.biz.domain.DeveloperConfig;
import com.hqpush.biz.domain.DeveloperMapper;
import com.hqpush.biz.domain.UsageMapper;
import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.stereotype.Component;

import java.nio.charset.StandardCharsets;
import java.util.Arrays;
import java.util.Comparator;

/** 开放面 AK/SK 鉴权（docs/11 §5：Timestamp ±300s + Nonce 防重放 + HMAC-SHA256 恒时比较）。
 *  StringToSign = METHOD \n PATH \n CANONICAL_QUERY \n TS \n NONCE \n SHA256_HEX(BODY)
 *  X-Signature  = HEX(HMAC-SHA256(app_secret, StringToSign))
 *  校验顺序：ak 存在 → 时间窗 → nonce（Redis SETNX 300s）→ 恒时比签。app_secret 兼容 v1: 密文。 */
@Component
public class OpenApiVerifier {

    static final long TS_WINDOW_MS = 300_000;
    private final DeveloperMapper devs;
    private final StringRedisTemplate redis;
    private final UsageMapper usage;
    private final long rateLimitPerMin;

    public OpenApiVerifier(DeveloperMapper devs, StringRedisTemplate redis, UsageMapper usage) {
        this.devs = devs;
        this.redis = redis;
        this.usage = usage;
        long limit = 600;
        try {
            String v = System.getenv("HQ_OPEN_RATE_LIMIT");
            if (v != null && !v.isBlank()) {
                limit = Long.parseLong(v.trim());
            }
        } catch (NumberFormatException ignored) {
            // 保持默认 600/min
        }
        this.rateLimitPerMin = limit;
    }

    /** 纯函数：签名串（客户端与服务端同构实现）。 */
    public static String stringToSign(String method, String path, String canonicalQuery,
                                      String ts, String nonce, String body) {
        return String.join("\n",
                method.toUpperCase(), path, canonicalQuery == null ? "" : canonicalQuery,
                ts, nonce, SecretBox.sha256Hex(body == null ? new byte[0] : body.getBytes(StandardCharsets.UTF_8)));
    }

    /** query 串规范化：参数按名字典序、值 RFC3986 风格编码（空格 %20）后 k=v& 拼接。 */
    public static String canonicalQuery(String rawQuery) {
        if (rawQuery == null || rawQuery.isBlank()) {
            return "";
        }
        return Arrays.stream(rawQuery.split("&"))
                .filter(p -> !p.isBlank())
                .map(p -> {
                    int eq = p.indexOf('=');
                    String k = eq < 0 ? p : p.substring(0, eq);
                    String v = eq < 0 ? "" : p.substring(eq + 1);
                    return new String[]{k, v};
                })
                .sorted(Comparator.comparing(a -> a[0]))
                .map(kv -> kv[0] + "=" + kv[1])
                .reduce((a, b) -> a + "&" + b)
                .orElse("");
    }

    /** 完整校验；通过返回开发者配置，失败抛 OpenApiException（code 对齐 docs/11 §3）。 */
    public DeveloperConfig verify(String method, String path, String rawQuery, String body,
                                  String accessKey, String ts, String nonce, String signature) {
        if (accessKey == null || accessKey.isBlank() || ts == null || nonce == null || signature == null) {
            throw new OpenApiException(40104, 401, "missing auth headers");
        }
        DeveloperConfig dev = devs.findByAccessKey(accessKey.trim());
        if (dev == null) {
            throw new OpenApiException(40104, 401, "unknown access key");
        }
        long tsMs;
        try {
            tsMs = Long.parseLong(ts.trim());
        } catch (NumberFormatException e) {
            throw new OpenApiException(40105, 401, "bad timestamp");
        }
        if (Math.abs(System.currentTimeMillis() - tsMs) > TS_WINDOW_MS) {
            throw new OpenApiException(40105, 401, "timestamp out of window");
        }
        String nonceKey = "open:nonce:" + accessKey.trim() + ":" + nonce.trim();
        Boolean first = redis.opsForValue().setIfAbsent(nonceKey, "1", java.time.Duration.ofSeconds(300));
        if (!Boolean.TRUE.equals(first)) {
            throw new OpenApiException(40106, 401, "nonce replayed");
        }

        String appSecret = dev.getAppSecret();
        if (appSecret.startsWith(SecretBox_PREFIX)) {
            appSecret = SecretBox.decrypt(appSecret); // v1: 密文（HQ_MASTER_KEYS 已配）
        }
        String expected = hmacSha256Hex(appSecret,
                stringToSign(method, path, canonicalQuery(rawQuery), ts.trim(), nonce.trim(), body == null ? "" : body));
        if (!signatureEquals(expected, signature.trim())) {
            throw new OpenApiException(40104, 401, "signature mismatch");
        }
        rateLimit(accessKey.trim());
        return dev;
    }

    /** per-AK 分钟级限流（Redis INCR + 60s 窗口；docs/12 B18 per-developer 限流）。 */
    private void rateLimit(String accessKey) {
        String window = String.valueOf(System.currentTimeMillis() / 60_000);
        String key = "open:rl:" + accessKey + ":" + window;
        Long n = redis.opsForValue().increment(key);
        if (n != null && n == 1L) {
            redis.expire(key, java.time.Duration.ofSeconds(60));
        }
        if (n != null && n > rateLimitPerMin) {
            throw new OpenApiException(42901, 429, "rate limit exceeded", 60); // 60s 滑窗配额
        }
    }

    public void recordUsage(String accessKey, String path, int statusCode, long latencyMs) {
        try {
            usage.insert(accessKey, path, statusCode, (int) Math.min(latencyMs, Integer.MAX_VALUE));
        } catch (Exception ignored) {
            // 计量失败不阻塞主请求（U1 口径；U3 计费切换前补补偿队列）
        }
    }

    static final String SecretBox_PREFIX = "v1:";

    private static String hmacSha256Hex(String secret, String data) {
        try {
            javax.crypto.Mac mac = javax.crypto.Mac.getInstance("HmacSHA256");
            mac.init(new javax.crypto.spec.SecretKeySpec(secret.getBytes(StandardCharsets.UTF_8), "HmacSHA256"));
            return bytesToHex(mac.doFinal(data.getBytes(StandardCharsets.UTF_8)));
        } catch (Exception e) {
            throw new IllegalStateException(e);
        }
    }

    private static String bytesToHex(byte[] d) {
        StringBuilder sb = new StringBuilder(d.length * 2);
        for (byte b : d) {
            sb.append(Character.forDigit((b >> 4) & 0xF, 16)).append(Character.forDigit(b & 0xF, 16));
        }
        return sb.toString();
    }

    private static boolean signatureEquals(String a, String b) {
        return java.security.MessageDigest.isEqual(
                a.getBytes(StandardCharsets.UTF_8), b.getBytes(StandardCharsets.UTF_8));
    }
}
