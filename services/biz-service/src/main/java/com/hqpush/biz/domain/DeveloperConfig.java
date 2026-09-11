package com.hqpush.biz.domain;

/** 开发者接入配置（api_developer_config，V4；app_secret 为 v1: 密文或旧明文）。 */
public class DeveloperConfig {
    private long userId;
    private String accessKey;
    private String appSecret;
    private String pushMode = "ws";
    private String hookUrl;
    private int pushSubscribeChanged;

    public long getUserId() { return userId; }
    public void setUserId(long userId) { this.userId = userId; }
    public String getAccessKey() { return accessKey; }
    public void setAccessKey(String accessKey) { this.accessKey = accessKey; }
    public String getAppSecret() { return appSecret; }
    public void setAppSecret(String appSecret) { this.appSecret = appSecret; }
    public String getPushMode() { return pushMode; }
    public void setPushMode(String pushMode) { this.pushMode = pushMode; }
    public String getHookUrl() { return hookUrl; }
    public void setHookUrl(String hookUrl) { this.hookUrl = hookUrl; }
    public int getPushSubscribeChanged() { return pushSubscribeChanged; }
    public void setPushSubscribeChanged(int v) { this.pushSubscribeChanged = v; }
}
