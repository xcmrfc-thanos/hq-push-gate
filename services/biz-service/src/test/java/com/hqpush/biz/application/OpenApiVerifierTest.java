package com.hqpush.biz.application;

import com.hqpush.biz.domain.DeveloperConfig;
import com.hqpush.biz.domain.DeveloperMapper;
import com.hqpush.biz.domain.UsageMapper;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.mockito.Mockito;
import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.data.redis.core.ValueOperations;

import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;
import java.nio.charset.StandardCharsets;
import java.time.Duration;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.Mockito.when;

/** 开放面签名验证单测：签名串同构 / 时间窗 / nonce 重放 / 恒时比签（docs/11 §5）。 */
class OpenApiVerifierTest {

    private static final String SECRET = "sk_test_1234567890abcdef";
    private static final String AK = "ak_test";

    private DeveloperMapper devs;
    private StringRedisTemplate redis;
    private ValueOperations<String, String> valueOps;
    private OpenApiVerifier verifier;

    @BeforeEach
    @SuppressWarnings("unchecked")
    void setup() {
        devs = Mockito.mock(DeveloperMapper.class);
        redis = Mockito.mock(StringRedisTemplate.class);
        valueOps = Mockito.mock(ValueOperations.class);
        Mockito.when(redis.opsForValue()).thenReturn(valueOps);
        when(valueOps.setIfAbsent(anyString(), anyString(), any(Duration.class))).thenReturn(true);
        when(valueOps.increment(anyString())).thenReturn(1L);
        UsageMapper usage = Mockito.mock(UsageMapper.class);

        verifier = new OpenApiVerifier(devs, redis, usage);
        DeveloperConfig dev = new DeveloperConfig();
        dev.setUserId(7);
        dev.setAccessKey(AK);
        dev.setAppSecret(SECRET); // legacy 明文形态
        when(devs.findByAccessKey(AK)).thenReturn(dev);
    }

    private String sign(String method, String path, String query, String ts, String nonce, String body) {
        return hmac(SECRET, OpenApiVerifier.stringToSign(method, path, query, ts, nonce, body));
    }

    private void verifyOk(String ts, String nonce) {
        verifier.verify("GET", "/open/v1/developer/config", "a=2&b=1", "",
                AK, ts, nonce, sign("GET", "/open/v1/developer/config", "a=2&b=1", ts, nonce, ""));
    }

    @Test
    void happyPathAndCanonicalQuery() {
        verifyOk(String.valueOf(System.currentTimeMillis()), "n1");
    }

    @Test
    void signatureMismatchRejected() {
        String ts = String.valueOf(System.currentTimeMillis());
        assertThrows(OpenApiException.class, () ->
                verifier.verify("GET", "/open/v1/developer/config", "a=1", "",
                        AK, ts, "nX", "deadbeef"));
    }

    @Test
    void timestampOutOfWindowRejected() {
        String old = String.valueOf(System.currentTimeMillis() - 400_000);
        OpenApiException e = assertThrows(OpenApiException.class, () -> verifyOk(old, "n2"));
        assertEquals(40105, e.getCode());
    }

    @Test
    void unknownAccessKeyRejected() {
        String ts = String.valueOf(System.currentTimeMillis());
        assertThrows(OpenApiException.class, () ->
                verifier.verify("GET", "/x", "", "", "ak_nobody", ts, "n3", "sig"));
    }

    @Test
    void nonceReplayRejected() {
        String ts = String.valueOf(System.currentTimeMillis());
        when(valueOps.setIfAbsent(anyString(), anyString(), any(Duration.class))).thenReturn(false);
        OpenApiException e = assertThrows(OpenApiException.class, () -> verifyOk(ts, "n4"));
        assertEquals(40106, e.getCode());
    }

    @Test
    void stringToSignShape() {
        String sts = OpenApiVerifier.stringToSign("get", "/p", "a=1", "123", "n", "body");
        assertEquals("GET\n/p\na=1\n123\nn\n" + SecretBox.sha256Hex("body".getBytes(StandardCharsets.UTF_8)), sts);
        // 空 query 与 null 等价
        assertEquals(OpenApiVerifier.stringToSign("GET", "/p", null, "123", "n", "b"),
                OpenApiVerifier.stringToSign("GET", "/p", "", "123", "n", "b"));
    }

    private static String hmac(String secret, String data) {
        try {
            Mac mac = Mac.getInstance("HmacSHA256");
            mac.init(new SecretKeySpec(secret.getBytes(StandardCharsets.UTF_8), "HmacSHA256"));
            StringBuilder sb = new StringBuilder();
            for (byte b : mac.doFinal(data.getBytes(StandardCharsets.UTF_8))) {
                sb.append(Character.forDigit((b >> 4) & 0xF, 16)).append(Character.forDigit(b & 0xF, 16));
            }
            return sb.toString();
        } catch (Exception e) {
            throw new IllegalStateException(e);
        }
    }
}
