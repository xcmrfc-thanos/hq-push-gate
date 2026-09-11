package com.hqpush.biz.application;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ObjectNode;
import com.hqpush.biz.domain.AlertRule;
import com.hqpush.biz.domain.AlertRuleMapper;
import com.hqpush.biz.domain.OutboxEvent;
import com.hqpush.biz.domain.UserVip;
import com.hqpush.biz.domain.UserVipMapper;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import java.util.List;

/**
 * 预警规则用例：CRUD 与 outbox 写入同事务（docs/08 §7.2：规则广播过大 -> RuleMsg 不带 user_ids）。
 * rule_bcast 由 outbox-publisher 异步发布，biz-service 不直接发 Kafka。
 */
@Service
public class RuleService {

    private final AlertRuleMapper mapper;
    private final UserVipMapper vip;
    private final ObjectMapper om = new ObjectMapper();

    public RuleService(AlertRuleMapper mapper, UserVipMapper vip) {
        this.mapper = mapper;
        this.vip = vip;
    }

    /** H5 入口配额（docs/10 §3.2）：共享总量+1 ≤ h5_max_sub（防 H5/API 叠加绕过）。 */
    private void checkH5Quota(long userId) {
        UserVip p = vip.findByUserId(userId);
        int h5Max = p == null ? 5 : p.getH5MaxSub();
        int total = mapper.countActiveByUser(userId);
        if (total + 1 > h5Max) {
            throw new QuotaExceededException("超出当前套餐最大订阅数量");
        }
    }

    public List<AlertRule> list(long userId) {
        return mapper.listByUser(userId);
    }

    public AlertRule get(long userId, long id) {
        AlertRule rule = mapper.findById(id);
        if (rule == null || rule.getUserId() != userId) {
            throw new NotFoundException("rule not found");
        }
        return rule;
    }

    @Transactional
    public AlertRule create(long userId, AlertRule in) {
        validate(in);
        checkH5Quota(userId);
        in.setId(mapper.maxId() + 1);
        in.setUserId(userId);
        if (in.getCooldownSec() <= 0) {
            in.setCooldownSec(60);
        }
        in.setStatus(1);
        mapper.insert(in);
        outbox(mapper.maxOutboxId() + 1, in.getId(), "RULE_UPSERT", in);
        return in;
    }

    @Transactional
    public AlertRule update(long userId, long id, AlertRule in) {
        AlertRule cur = get(userId, id);
        validate(in);
        cur.setCondition(in.getCondition());
        cur.setCooldownSec(in.getCooldownSec() > 0 ? in.getCooldownSec() : cur.getCooldownSec());
        mapper.update(cur);
        outbox(mapper.maxOutboxId() + 1, id, "RULE_UPSERT", cur);
        return cur;
    }

    @Transactional
    public AlertRule updateStatus(long userId, long id, int status) {
        AlertRule cur = get(userId, id);
        mapper.updateStatus(id, status == 1 ? 1 : 0);
        cur.setStatus(status == 1 ? 1 : 0);
        outbox(mapper.maxOutboxId() + 1, id, "RULE_UPSERT", cur);
        return cur;
    }

    @Transactional
    public void delete(long userId, long id) {
        get(userId, id);
        mapper.delete(id);
        OutboxEvent e = new OutboxEvent();
        e.setId(mapper.maxOutboxId() + 1);
        e.setAggregateId(id);
        e.setEventType("RULE_DELETE");
        e.setEventKey("rule:" + id + ":v" + System.currentTimeMillis());
        e.setPayload("{\"ruleId\":" + id + ",\"op\":\"DELETE\"}");
        mapper.insertOutbox(e);
    }

    private void validate(AlertRule in) {
        if (in.getCondition() == null || in.getCondition().isBlank()) {
            throw new BadRequestException("condition required");
        }
        if (in.getMarket() == null || in.getSymbol() == null) {
            throw new BadRequestException("market/symbol required");
        }
    }

    /** outbox payload 与 RuleMsg 公共契约对齐（rule_id、version、op、market、symbol、rule_type、condition、cooldown_sec）。 */
    private void outbox(long outboxId, long ruleId, String eventType, AlertRule rule) {
        ObjectNode payload = om.createObjectNode()
                .put("ruleId", ruleId)
                .put("version", rule.getVersion())
                .put("op", eventType.equals("RULE_DELETE") ? "DELETE" : "UPSERT")
                .put("market", rule.getMarket())
                .put("symbol", rule.getSymbol())
                .put("ruleType", rule.getRuleType())
                .put("condition", rule.getCondition())
                .put("cooldownSec", rule.getCooldownSec());
        OutboxEvent e = new OutboxEvent();
        e.setId(outboxId);
        e.setAggregateId(ruleId);
        e.setEventType(eventType);
        e.setEventKey("rule:" + ruleId + ":v" + rule.getVersion());
        e.setPayload(payload.toString());
        mapper.insertOutbox(e);
    }

    public static class NotFoundException extends RuntimeException {
        public NotFoundException(String msg) { super(msg); }
    }

    public static class BadRequestException extends RuntimeException {
        public BadRequestException(String msg) { super(msg); }
    }

    /** 配额超限（docs/10 §3.2：共享总量 +1 > h5_max_sub）。 */
    public static class QuotaExceededException extends RuntimeException {
        public QuotaExceededException(String msg) { super(msg); }
    }
}
