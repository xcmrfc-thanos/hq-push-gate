package com.hqpush.biz.domain;

import java.util.Date;

/** 用户套餐实体（user_vip，docs/10 §8；无记录时按 free 默认值）。 */
public class UserVip {
    private long userId;
    private String planType = "free";
    private Date expireTime;
    private int h5MaxSub = 5;
    private int apiMaxSub = 20;
    private boolean allowSms;
    private String channelDefault = "email";

    public long getUserId() { return userId; }
    public void setUserId(long userId) { this.userId = userId; }
    public String getPlanType() { return planType; }
    public void setPlanType(String planType) { this.planType = planType; }
    public Date getExpireTime() { return expireTime; }
    public void setExpireTime(Date expireTime) { this.expireTime = expireTime; }
    public int getH5MaxSub() { return h5MaxSub; }
    public void setH5MaxSub(int h5MaxSub) { this.h5MaxSub = h5MaxSub; }
    public int getApiMaxSub() { return apiMaxSub; }
    public void setApiMaxSub(int apiMaxSub) { this.apiMaxSub = apiMaxSub; }
    public boolean isAllowSms() { return allowSms; }
    public void setAllowSms(boolean allowSms) { this.allowSms = allowSms; }
    public String getChannelDefault() { return channelDefault; }
    public void setChannelDefault(String channelDefault) { this.channelDefault = channelDefault; }
}
