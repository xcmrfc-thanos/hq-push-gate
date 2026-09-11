package com.hqpush.biz.domain;

/** outbox_event 行（业务事务与 Kafka rule_bcast 的可靠桥接）。 */
public class OutboxEvent {
    private long id;
    private long aggregateId;
    private String eventType;   // RULE_UPSERT / RULE_DELETE
    private String eventKey;
    private String payload;     // 与 RuleMsg 公共契约对应的业务载荷

    public long getId() { return id; }
    public void setId(long id) { this.id = id; }
    public long getAggregateId() { return aggregateId; }
    public void setAggregateId(long aggregateId) { this.aggregateId = aggregateId; }
    public String getEventType() { return eventType; }
    public void setEventType(String eventType) { this.eventType = eventType; }
    public String getEventKey() { return eventKey; }
    public void setEventKey(String eventKey) { this.eventKey = eventKey; }
    public String getPayload() { return payload; }
    public void setPayload(String payload) { this.payload = payload; }
}
