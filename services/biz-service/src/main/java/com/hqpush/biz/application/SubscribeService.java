package com.hqpush.biz.application;

import com.hqpush.biz.domain.AlertRule;
import com.hqpush.biz.domain.OutboxEvent;
import com.hqpush.biz.domain.SymbolMetaMapper;
import com.hqpush.biz.domain.UserVip;
import com.hqpush.biz.domain.UserVipMapper;
import com.hqpush.biz.domain.AlertRuleMapper;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import java.util.List;
import java.util.UUID;

/**
 * 订阅糖衣用例（docs/10 §3.2/§11.5）：subscribe API → alert_rule（source=api）。
 *
 * 配额口径（防绕过）：H5 与 API 共享总量 count(alert_rule status=1)；
 * API 新增要求 total+1 ≤ api_max_sub（H5 入口在 RuleService 校验 h5_max_sub）。
 * 免费套餐（allow_sms=0）拒绝 channelPrefer=sms。
 * symbol_meta 存在性校验（docs/10 §3.2 第 6 步，Step1 D10-1 补实现）。
 */
@Service
public class SubscribeService {

    private final AlertRuleMapper rules;
    private final UserVipMapper vip;
    private final SymbolMetaMapper symbolMeta;
    private final VipCache cache;

    public SubscribeService(AlertRuleMapper rules, UserVipMapper vip, SymbolMetaMapper symbolMeta, VipCache cache) {
        this.rules = rules;
        this.vip = vip;
        this.symbolMeta = symbolMeta;
        this.cache = cache;
    }

    /** 标的存在性校验（symbol_meta 且 status=1 上市；docs/10 §3.2 第 6 步）。 */
    public void checkSymbol(String market, String symbol) {
        if (symbolMeta.countListed(market, symbol) == 0) {
            throw new BadRequestException("unknown symbol: " + market + " " + symbol);
        }
    }

    /** 套餐读取：无 user_vip 记录按 free 默认（docs/10 §3.1）。B22：优先 Redis 缓存（VipCache），
     *  未命中回源 MySQL 并回填。 */
    public UserVip planOf(long userId) {
        String pt = cache.planType(userId);
        if (pt != null) {
            return VipCache.materialize(userId, pt);
        }
        UserVip v = vip.findByUserId(userId);
        if (v == null) {
            v = new UserVip();
            v.setUserId(userId);
            v.setPlanType("free");
            v.setH5MaxSub(5);
            v.setApiMaxSub(20);
            v.setAllowSms(false);
            v.setChannelDefault("email");
        } else {
            cache.set(userId, v.getPlanType(), v.getExpireTime()); // 回填缓存（TTL 5min）
        }
        return v;
    }

    /** 配额：共享总量对齐本次入口的套餐上限（防 H5/API 叠加绕过）。 */
    public void checkQuota(long userId, String entrySource) {
        UserVip p = planOf(userId);
        int cap = "api".equals(entrySource) ? p.getApiMaxSub() : p.getH5MaxSub();
        int total = vip.countActiveByUser(userId);
        if (total + 1 > cap) {
            throw new QuotaExceededException("超出当前套餐最大订阅数量");
        }
    }

    /** 渠道偏好校验：免费套餐（allow_sms=0）拒绝 sms。 */
    public void checkChannelPrefer(long userId, String channelPrefer) {
        if (!"sms".equals(channelPrefer)) {
            return;
        }
        UserVip p = planOf(userId);
        if (!p.isAllowSms()) {
            throw new SmsNotAllowedException("当前套餐不支持短信通道");
        }
    }

    /** 创建订阅：rise/fall 阈值（小数比例，0.05=5%）→ PCT_CHANGE 规则（direction 推导）。 */
    @Transactional
    public AlertRule subscribe(long userId, String stockCode, Double riseThreshold,
                               Double fallThreshold, String channelPrefer) {
        if (stockCode == null || !stockCode.matches("\\d{6}")) {
            throw new BadRequestException("invalid stockCode");
        }
        checkSymbol("A_SHARE", stockCode); // 存在性校验（docs/10 §3.2 第 6 步，D10-1）
        checkChannelPrefer(userId, channelPrefer);
        checkQuota(userId, "api");

        String direction;
        if (riseThreshold != null && riseThreshold > 0 && (fallThreshold == null || fallThreshold >= 0)) {
            direction = "up";
        } else if (fallThreshold != null && fallThreshold < 0 && (riseThreshold == null || riseThreshold <= 0)) {
            direction = "down";
        } else {
            direction = "both";
        }
        double th = 3.0; // 默认 ±3%
        if (riseThreshold != null && riseThreshold > 0) {
            th = riseThreshold * 100;
        } else if (fallThreshold != null && fallThreshold < 0) {
            th = -fallThreshold * 100;
        }

        AlertRule r = new AlertRule();
        r.setId(rules.maxId() + 1);
        r.setUserId(userId);
        r.setMarket("A_SHARE");
        r.setSymbol(stockCode);
        r.setRuleType("PCT_CHANGE");
        r.setCondition("{\"threshold\":" + th + ",\"direction\":\"" + direction + "\"}");
        r.setCooldownSec(300); // 订阅类默认 5 分钟防抖（docs/10 §4.3）
        r.setStatus(1);
        r.setVersion(1);
        r.setChannelPrefer("sms".equalsIgnoreCase(channelPrefer) ? "sms"
                : "email".equalsIgnoreCase(channelPrefer) ? "email" : null);
        r.setSource("api");
        r.setSubscribeKey(UUID.randomUUID().toString());
        rules.insertSubscribe(r);
        outboxUpsert(r);
        return r;
    }

    /** 退订：按 subscribeKey 停用（规则保留审计，status=0 + bcast 撤销）。 */
    public boolean unsubscribe(long userId, String subscribeKey) {
        AlertRule cur = rules.findBySubscribeKey(subscribeKey);
        if (cur == null || cur.getUserId() != userId) {
            throw new NotFoundException("subscription not found");
        }
        rules.updateStatus(cur.getId(), 0);
        outboxDelete(cur);
        return true;
    }

    /** 订阅列表（source=api，投影订阅语义字段）。 */
    public List<AlertRule> list(long userId) {
        return rules.listActiveBySource(userId, "api");
    }

    /** 按 subscribeKey 查询本人订阅（含归属校验；不存在/非本人返回 null）。 */
    public AlertRule findByKey(long userId, String subscribeKey) {
        AlertRule cur = rules.findBySubscribeKey(subscribeKey);
        return cur != null && cur.getUserId() == userId ? cur : null;
    }

    /** 退订（按 subscribeKey）：归属校验后停用 + bcast 撤销。 */
    @Transactional
    public void unsubscribeByKey(long userId, String subscribeKey) {
        AlertRule cur = findByKey(userId, subscribeKey);
        if (cur == null) {
            throw new NotFoundException("subscription not found");
        }
        unsubscribeById(userId, cur.getId());
    }

    /** 退订（按规则 ID，限本人）：停用 + bcast 撤销（B17 补齐 Branch 11 缺失实现）。 */
    @Transactional
    public void unsubscribeById(long userId, long ruleId) {
        AlertRule cur = rules.findById(ruleId);
        if (cur == null || cur.getUserId() != userId) {
            throw new NotFoundException("subscription not found");
        }
        rules.updateStatus(cur.getId(), 0);
        outboxDelete(cur);
    }

    /** API 订阅列表（source=api）。 */
    public List<AlertRule> listApi(long userId) {
        return rules.listActiveBySource(userId, "api");
    }

    public static class QuotaExceededException extends RuntimeException {
        public QuotaExceededException(String msg) { super(msg); }
    }

    public static class SmsNotAllowedException extends RuntimeException {
        public SmsNotAllowedException(String msg) { super(msg); }
    }

    public static class BadRequestException extends RuntimeException {
        public BadRequestException(String msg) { super(msg); }
    }

    public static class NotFoundException extends RuntimeException {
        public NotFoundException(String msg) { super(msg); }
    }

    /** outbox payload 与 RuleMsg 公共契约对齐（同 RuleService，docs/04 RuleMsg）。 */
    private OutboxEvent outbox(long outboxId, long ruleId, String eventType, AlertRule rule) {
        OutboxEvent e = new OutboxEvent();
        e.setId(outboxId);
        e.setAggregateId(ruleId);
        e.setEventType(eventType);
        e.setEventKey("rule:" + ruleId + ":v" + rule.getVersion());
        e.setPayload("{\"ruleId\":" + ruleId
                + ",\"version\":" + rule.getVersion()
                + ",\"op\":\"" + (eventType.equals("RULE_DELETE") ? "DELETE" : "UPSERT")
                + "\",\"market\":\"" + rule.getMarket()
                + "\",\"symbol\":\"" + rule.getSymbol()
                + "\",\"ruleType\":\"" + rule.getRuleType()
                + "\",\"condition\":" + jsonQuote(rule.getCondition())
                + ",\"cooldownSec\":" + rule.getCooldownSec() + "}");
        return e;
    }

    private String jsonQuote(String s) {
        return "\"" + s.replace("\\", "\\\\").replace("\"", "\\\"") + "\"";
    }

    private void outboxUpsert(AlertRule r) {
        rules.insertOutbox(outbox(rules.maxOutboxId() + 1, r.getId(), "RULE_UPSERT", r));
    }

    private void outboxDelete(AlertRule r) {
        OutboxEvent e = new OutboxEvent();
        e.setId(rules.maxOutboxId() + 1);
        e.setEventType("RULE_DELETE");
        e.setEventKey("rule:" + r.getId() + ":v" + (r.getVersion() + 1));
        e.setPayload("{\"ruleId\":" + r.getId() + ",\"op\":\"DELETE\"}");
        rules.insertOutbox(e);
    }
}
