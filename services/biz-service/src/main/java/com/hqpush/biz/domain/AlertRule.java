package com.hqpush.biz.domain;

/** 预警规则实体（alert_rule）。 */
public class AlertRule {
    private long id;
    private long userId;
    private String market;
    private String symbol;
    private String ruleType;
    private String condition;   // 与 RuleMsg.condition 一致的结构化 JSON
    private int cooldownSec;
    private int status;
    private long version;
    private String channelPrefer; // sms|email|NULL（NULL=继承套餐默认，docs/10 §3.3）
    private String source;        // h5|api
    private String subscribeKey;  // subscribe API 订阅标识

    public long getId() { return id; }
    public void setId(long id) { this.id = id; }
    public long getUserId() { return userId; }
    public void setUserId(long userId) { this.userId = userId; }
    public String getMarket() { return market; }
    public void setMarket(String market) { this.market = market; }
    public String getSymbol() { return symbol; }
    public void setSymbol(String symbol) { this.symbol = symbol; }
    public String getRuleType() { return ruleType; }
    public void setRuleType(String ruleType) { this.ruleType = ruleType; }
    public String getCondition() { return condition; }
    public void setCondition(String condition) { this.condition = condition; }
    public int getCooldownSec() { return cooldownSec; }
    public void setCooldownSec(int cooldownSec) { this.cooldownSec = cooldownSec; }
    public int getStatus() { return status; }
    public void setStatus(int status) { this.status = status; }
    public long getVersion() { return version; }
    public void setVersion(long version) { this.version = version; }
    public String getChannelPrefer() { return channelPrefer; }
    public void setChannelPrefer(String channelPrefer) { this.channelPrefer = channelPrefer; }
    public String getSource() { return source; }
    public void setSource(String source) { this.source = source; }
    public String getSubscribeKey() { return subscribeKey; }
    public void setSubscribeKey(String subscribeKey) { this.subscribeKey = subscribeKey; }
}
