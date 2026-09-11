package com.hqpush.biz.domain;

import org.apache.ibatis.annotations.*;

import java.util.List;

/** 预警规则 Mapper（alert_rule 表 + outbox_event 同事务写入，docs/08 §7.2）。 */
@Mapper
public interface AlertRuleMapper {

    @Select("SELECT id, user_id AS userId, market, symbol, rule_type AS ruleType, " +
            "`condition` AS `condition`, cooldown_sec AS cooldownSec, status, version " +
            "FROM alert_rule WHERE id = #{id}")
    AlertRule findById(@Param("id") long id);

    @Select("SELECT id, user_id AS userId, market, symbol, rule_type AS ruleType, " +
            "`condition` AS `condition`, cooldown_sec AS cooldownSec, status, version " +
            "FROM alert_rule WHERE user_id = #{userId} ORDER BY id DESC LIMIT 200")
    List<AlertRule> listByUser(@Param("userId") long userId);

    @Insert("INSERT INTO alert_rule (id, user_id, market, symbol, rule_type, `condition`, cooldown_sec, status, version) " +
            "VALUES (#{id}, #{userId}, #{market}, #{symbol}, #{ruleType}, #{condition}, #{cooldownSec}, 1, 1)")
    int insert(AlertRule rule);

    @Update("UPDATE alert_rule SET `condition` = #{condition}, cooldown_sec = #{cooldownSec}, " +
            "version = version + 1 WHERE id = #{id}")
    int update(AlertRule rule);

    @Update("UPDATE alert_rule SET status = #{status}, version = version + 1 WHERE id = #{id}")
    int updateStatus(@Param("id") long id, @Param("status") int status);

    @Delete("DELETE FROM alert_rule WHERE id = #{id}")
    int delete(@Param("id") long id);

    @Select("SELECT IFNULL(MAX(id),0) FROM alert_rule")
    long maxId();

    // ---- 订阅糖衣（docs/10 §3.3/§11.5：source=api 的 alert_rule 投影）----

    @Select("SELECT COUNT(*) FROM alert_rule WHERE user_id = #{userId} AND status = 1")
    int countActiveByUser(@Param("userId") long userId);

    @Insert("INSERT INTO alert_rule (id, user_id, market, symbol, rule_type, `condition`, " +
            "cooldown_sec, status, version, channel_prefer, source, subscribe_key) " +
            "VALUES (#{id}, #{userId}, #{market}, #{symbol}, #{ruleType}, #{condition}, " +
            "#{cooldownSec}, 1, #{version}, #{channelPrefer}, #{source}, #{subscribeKey})")
    int insertSubscribe(AlertRule rule);

    @Select("SELECT id, user_id AS userId, market, symbol, rule_type AS ruleType, " +
            "`condition` AS `condition`, cooldown_sec AS cooldownSec, status, version, " +
            "channel_prefer AS channelPrefer, source, subscribe_key " +
            "FROM alert_rule WHERE subscribe_key = #{subscribeKey}")
    AlertRule findBySubscribeKey(@Param("subscribeKey") String subscribeKey);

    @Select("SELECT id, user_id AS userId, market, symbol, rule_type AS ruleType, " +
            "`condition` AS `condition`, cooldown_sec AS cooldownSec, status, version, " +
            "channel_prefer AS channelPrefer, source, subscribe_key " +
            "FROM alert_rule WHERE user_id = #{userId} AND source = #{source} AND status = 1 " +
            "ORDER BY id DESC LIMIT 500")
    java.util.List<AlertRule> listActiveBySource(@Param("userId") long userId, @Param("source") String source);

    // ---- outbox：与规则变更同一事务，由 outbox-publisher 发布 rule_bcast（U0 不启用 Debezium）----

    @Insert("INSERT INTO outbox_event (id, aggregate_type, aggregate_id, event_type, event_key, payload, " +
            "status, retry_count, next_retry_at) " +
            "VALUES (#{id}, 'RULE', #{aggregateId}, #{eventType}, #{eventKey}, #{payload}, 'PENDING', 0, NOW(3))")
    int insertOutbox(OutboxEvent event);

    @Select("SELECT IFNULL(MAX(id),0) FROM outbox_event")
    long maxOutboxId();
}
