package com.hqpush.biz.application;

import com.hqpush.biz.domain.*;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import java.util.Date;
import java.util.regex.Pattern;

/** 商业化闭环（docs/12 B19）：套餐发放/续期（管理端）+ 自助绑定（用户面）。
 *  档位数值以 docs/10 §3.1 为准（free 5/20 · vip1 10/50 · vip2 20/100 · vip3 50/300），
 *  B19 首次把 vip1~3 档位落代码（原仅文档）。 */
@Service
public class CommerceService {

    private static final Pattern PHONE = Pattern.compile("^1[3-9]\\d{9}$");
    private static final Pattern EMAIL = Pattern.compile("^[^@\\s]+@[^@\\s]+\\.[^@\\s]+$");

    public record Tier(int h5, int api, boolean sms) {
    }

    public static Tier tier(String planType) {
        return switch (planType == null ? "free" : planType) {
            case "vip1" -> new Tier(10, 50, true);
            case "vip2" -> new Tier(20, 100, true);
            case "vip3" -> new Tier(50, 300, true);
            default -> new Tier(5, 20, false);
        };
    }

    /** 套餐读取（无记录按 free 默认值，docs/10 §3.1）。B22：优先 Redis 缓存（VipCache），
     *  未命中回源 MySQL 并回填（配额按 tier() 推导，与落库值一致——档位硬编码于业务层）。 */
    public UserVip planOf(long userId) {
        String pt = cache.planType(userId);
        if (pt != null) {
            return VipCache.materialize(userId, pt);
        }
        UserVip v = vip.findByUserId(userId);
        if (v == null) {
            v = freeUserVip(userId);
        } else {
            cache.set(userId, v.getPlanType(), v.getExpireTime()); // 回填缓存（TTL 5min）
        }
        return v;
    }

    private static UserVip freeUserVip(long userId) {
        UserVip v = new UserVip();
        v.setUserId(userId);
        v.setPlanType("free");
        v.setH5MaxSub(5);
        v.setApiMaxSub(20);
        v.setAllowSms(false);
        v.setChannelDefault("email");
        return v;
    }

    private final UserVipMapper vip;
    private final UserMapper users;
    private final AuditMapper audit;
    private final VipCache cache;

    public CommerceService(UserVipMapper vip, UserMapper users, AuditMapper audit, VipCache cache) {
        this.vip = vip;
        this.users = users;
        this.audit = audit;
        this.cache = cache;
    }

    /** 管理端发放/变更套餐（操作审计留痕，operator=管理端标识）。 */
    @Transactional
    public UserVip grant(long userId, String planType, Date expire, String operator) {
        if (!tierSupported(planType)) {
            throw new RuleService.BadRequestException("plan_type must be free|vip1|vip2|vip3");
        }
        Tier t = tier(planType);
        UserVip v = new UserVip();
        v.setUserId(userId);
        v.setPlanType(planType);
        v.setExpireTime(expire);
        v.setH5MaxSub(t.h5());
        v.setApiMaxSub(t.api());
        v.setAllowSms(t.sms());
        v.setChannelDefault("email");
        vip.upsert(v);
        cache.set(userId, planType, expire); // B22：套餐变更即时写入缓存（复核器 5min 内生效）
        audit.insert(operator == null || operator.isBlank() ? "admin" : operator, "grant_plan", "user",
                String.valueOf(userId), "plan=" + planType + " expire=" + (expire == null ? "-" : expire));
        return v;
    }

    /** 自助绑定（用户面，JWT）：email/phone 严格格式校验（违规即 400）。 */
    public void bindEmail(long userId, String email) {
        if (email == null || !EMAIL.matcher(email).matches()) {
            throw new RuleService.BadRequestException("invalid email");
        }
        users.updateEmail(userId, email);
    }

    public void bindPhone(long userId, String phone) {
        if (phone == null || !PHONE.matcher(phone).matches()) {
            throw new RuleService.BadRequestException("invalid phone (1[3-9]xxxxxxxxx)");
        }
        users.updatePhone(userId, phone);
    }

    /** 自助绑定钉钉/飞书群机器人（格式 URL|secret|keyword；空字符串解绑）。
     *  channelDefault 可选（sms|email|dd|feishu，docs/10 §3.1）——绑定 IM 后即可切默认通道，
     *  打通 dispatcher 的 dd/feishu 路径（Step1 D10-3 死路径修复）。 */
    public void bindWebhooks(long userId, String dingtalk, String feishu, String channelDefault) {
        dingtalk = safeWebhook(dingtalk);
        feishu = safeWebhook(feishu);
        String cd = channelDefault == null || channelDefault.isBlank() ? null : channelDefault.trim();
        if (cd != null && !java.util.List.of("sms", "email", "dd", "feishu").contains(cd)) {
            throw new RuleService.BadRequestException("invalid channel_default");
        }
        vip.updateWebhooks(userId, dingtalk, feishu, cd == null ? "email" : cd);
    }

    private static String safeWebhook(String raw) {
        if (raw == null || raw.isBlank()) {
            return "";
        }
        String url = raw.trim();
        if (!url.startsWith("https://")) {
            throw new RuleService.BadRequestException("webhook must start with https://");
        }
        if (url.length() > 512) {
            throw new RuleService.BadRequestException("webhook too long");
        }
        return url;
    }

    /** 到期降级（docs/12 §2.2 B19 收尾）：expire_time 过期的非 free 套餐批量降为 free（停短信）。
     *  由 CommerceScheduler 定时触发；每用户一条审计记录。 */
    public int expireOverdue() {
        java.util.Date now = new java.util.Date();
        java.util.List<Long> expired = vip.findExpired(now);
        int n = 0;
        for (long uid : expired) {
            Tier t = tier("free");
            UserVip v = new UserVip();
            v.setUserId(uid);
            v.setPlanType("free");
            v.setH5MaxSub(t.h5());
            v.setApiMaxSub(t.api());
            v.setAllowSms(false);
            v.setChannelDefault("email");
            vip.upsert(v);
            cache.set(uid, "free", null); // B22：降级即时写缓存，ws-gateway 复核器下周期摘权
            audit.insert("scheduler", "expire_downgrade", "user", String.valueOf(uid), "expire_time passed -> free");
            n++;
        }
        return n;
    }

    private static boolean tierSupported(String p) {
        return "free".equals(p) || "vip1".equals(p) || "vip2".equals(p) || "vip3".equals(p);
    }
}
