package com.hqpush.biz.application;

import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.stereotype.Component;

import java.time.Duration;
import java.util.Date;

/**
 * user_vip Redis 缓存（B22：套餐复核器数据源 + 销 docs/12 §2.6 U2 项）。
 * 键 {@code user_vip:{id}}，值格式 {@code planType|expireEpochSec}（expire 空/0 = 无期限）；
 * 写路径（grant / expireOverdue / planOf 回填）维护，TTL 5min（ADR-040 复核周期）。
 * ws-gateway 复核器按同一格式读取（repository.RedisUserVipStore）。
 */
@Component
public class VipCache {

    public static final String PREFIX = "user_vip:";
    private static final Duration TTL = Duration.ofMinutes(5);

    private final StringRedisTemplate redis;

    public VipCache(StringRedisTemplate redis) {
        this.redis = redis;
    }

    /** 读缓存套餐；无缓存/格式非法返回 null；expire 已过按 free（与 ws-gateway 判定一致）。 */
    public String planType(long userId) {
        String v = redis.opsForValue().get(PREFIX + userId);
        if (v == null || v.isEmpty()) {
            return null;
        }
        String pt = v;
        long exp = 0;
        int bar = v.indexOf('|');
        if (bar >= 0) {
            pt = v.substring(0, bar);
            String es = v.substring(bar + 1);
            if (!es.isEmpty()) {
                try {
                    exp = Long.parseLong(es);
                } catch (NumberFormatException e) {
                    return null;
                }
            }
        }
        if (pt.isEmpty()) {
            return null;
        }
        if (exp > 0 && exp * 1000L <= System.currentTimeMillis()) {
            return "free";
        }
        return pt;
    }

    /** 写缓存（grant / expireOverdue / planOf 回填共用；expire=null 表示无期限）。 */
    public void set(long userId, String planType, Date expire) {
        long exp = expire == null ? 0 : expire.getTime() / 1000;
        redis.opsForValue().set(PREFIX + userId, planType + "|" + (exp == 0 ? "" : exp), TTL);
    }

    /** 由套餐档位物化 UserVip（配额按 docs/10 §3.1 硬编码档位推导；expire 不落对象，到期判定在 planType 已处理）。 */
    public static com.hqpush.biz.domain.UserVip materialize(long userId, String planType) {
        CommerceService.Tier t = CommerceService.tier(planType);
        com.hqpush.biz.domain.UserVip v = new com.hqpush.biz.domain.UserVip();
        v.setUserId(userId);
        v.setPlanType(planType);
        v.setH5MaxSub(t.h5());
        v.setApiMaxSub(t.api());
        v.setAllowSms(t.sms());
        v.setChannelDefault("email");
        return v;
    }
}
