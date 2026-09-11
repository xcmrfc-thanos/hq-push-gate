package com.hqpush.biz.application;

import io.jsonwebtoken.Claims;
import io.jsonwebtoken.Jwts;
import io.jsonwebtoken.security.Keys;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.stereotype.Service;

import javax.crypto.SecretKey;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.Date;
import java.util.Map;

/** JWT 签发与校验（HS256；APISIX 边缘校验与 ws-gateway Upgrade 复核共用密钥）。
 *  claim 契约（docs/11 §7.1，B17 三方对齐）：sub=uid 字符串（保留）、uid=数值（ws-gateway）、
 *  typ=access|refresh（三端都验）、key=APISIX jwt-auth consumer key（hq.jwt.consumer-key）。 */
@Service
public class TokenService {

    private final SecretKey key;
    private final String consumerKey;
    private final Duration accessTtl = Duration.ofHours(2);
    private final Duration refreshTtl = Duration.ofDays(14);

    public TokenService(@Value("${hq.jwt.secret:dev-jwt-secret}") String secret,
                        @Value("${hq.jwt.consumer-key:hqweb-key}") String consumerKey,
                        @Value("${hq.profile:dev}") String profile) {
        if ("prod".equals(profile) && (secret == null || secret.isBlank() || "dev-jwt-secret".equals(secret))) {
            throw new IllegalStateException(
                    "hq.profile=prod 要求通过环境变量注入强 hq.jwt.secret（禁止 dev 默认值，docs/12 §1.1 #18）");
        }
        byte[] padded = pad(secret.getBytes(StandardCharsets.UTF_8), 32);
        this.key = Keys.hmacShaKeyFor(padded);
        this.consumerKey = consumerKey;
    }

    public String issueAccess(long uid) {
        return build(String.valueOf(uid), accessTtl,
                Map.of("typ", "access", "uid", uid, "key", consumerKey));
    }

    public String issueRefresh(long uid) {
        return build(String.valueOf(uid), refreshTtl,
                Map.of("typ", "refresh", "uid", uid, "key", consumerKey));
    }

    /** 校验并返回 uid；type 为期望类型（access/refresh）。 */
    public long verify(String token, String type) {
        Claims claims = Jwts.parser().verifyWith(key).build()
                .parseSignedClaims(token).getPayload();
        if (!type.equals(claims.get("typ", String.class))) {
            throw new IllegalArgumentException("token type mismatch");
        }
        return Long.parseLong(claims.getSubject());
    }

    private String build(String subject, Duration ttl, Map<String, Object> claims) {
        var builder = Jwts.builder()
                .subject(subject)
                .claims(claims)
                .issuedAt(new Date())
                .expiration(new Date(System.currentTimeMillis() + ttl.toMillis()))
                .signWith(key);
        return builder.compact();
    }

    /** HS256 要求至少 256 位密钥。 */
    private static byte[] pad(byte[] raw, int min) {
        if (raw.length >= min) {
            return raw;
        }
        byte[] out = new byte[min];
        System.arraycopy(raw, 0, out, 0, raw.length);
        for (int i = raw.length; i < min; i++) {
            out[i] = '0';
        }
        return out;
    }
}
