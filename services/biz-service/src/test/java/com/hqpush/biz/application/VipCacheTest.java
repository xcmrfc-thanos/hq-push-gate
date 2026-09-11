package com.hqpush.biz.application;

import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.mockito.Mockito;
import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.data.redis.core.ValueOperations;

import java.time.Duration;
import java.util.Date;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

/** user_vip 缓存（B22）：值格式 planType|expireEpochSec 解析 / 过期按 free / 写格式 / 档位物化。 */
class VipCacheTest {

    private StringRedisTemplate redis;
    private ValueOperations<String, String> valueOps;
    private VipCache cache;

    @BeforeEach
    @SuppressWarnings("unchecked")
    void setup() {
        redis = Mockito.mock(StringRedisTemplate.class);
        valueOps = Mockito.mock(ValueOperations.class);
        when(redis.opsForValue()).thenReturn(valueOps);
        cache = new VipCache(redis);
    }

    @Test
    void parsesPlanTypeAndExpire() {
        long future = (System.currentTimeMillis() + 3_600_000) / 1000; // 1 小时后
        when(valueOps.get("user_vip:1")).thenReturn("vip1|" + future);
        assertEquals("vip1", cache.planType(1));

        when(valueOps.get("user_vip:2")).thenReturn("free|");
        assertEquals("free", cache.planType(2));

        when(valueOps.get("user_vip:3")).thenReturn("vip2");
        assertEquals("vip2", cache.planType(3));
    }

    @Test
    void expiredExpireTreatsAsFree() {
        long past = (System.currentTimeMillis() - 60_000) / 1000; // 1 分钟前过期
        when(valueOps.get("user_vip:1")).thenReturn("vip3|" + past);
        assertEquals("free", cache.planType(1));
    }

    @Test
    void cacheMissAndMalformedReturnNull() {
        when(valueOps.get("user_vip:1")).thenReturn(null);
        assertNull(cache.planType(1));

        when(valueOps.get("user_vip:2")).thenReturn("|123");
        assertNull(cache.planType(2));

        when(valueOps.get("user_vip:3")).thenReturn("vip1|abc");
        assertNull(cache.planType(3));
    }

    @Test
    void writesPlanTypeBarExpireFormat() {
        long future = (System.currentTimeMillis() + 3_600_000) / 1000;
        cache.set(7, "vip1", new Date(future * 1000));
        verify(valueOps).set("user_vip:7", "vip1|" + future, Duration.ofMinutes(5));

        cache.set(8, "free", null);
        verify(valueOps).set("user_vip:8", "free|", Duration.ofMinutes(5));
    }

    @Test
    void materializeDerivesTier() {
        var v = VipCache.materialize(1, "vip1");
        assertEquals("vip1", v.getPlanType());
        assertEquals(10, v.getH5MaxSub());
        assertEquals(50, v.getApiMaxSub());
        assertTrue(v.isAllowSms());

        var f = VipCache.materialize(2, "free");
        assertEquals(5, f.getH5MaxSub());
        assertEquals(20, f.getApiMaxSub());
        assertFalse(f.isAllowSms());
    }
}
