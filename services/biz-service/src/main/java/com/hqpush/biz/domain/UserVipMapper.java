package com.hqpush.biz.domain;

import org.apache.ibatis.annotations.*;

/** 用户套餐 Mapper（docs/10 §8；无记录按 free 默认值，service 层兜底）。B19 补写路径。 */
@Mapper
public interface UserVipMapper {

    @Select("SELECT user_id AS userId, plan_type AS planType, expire_time AS expireTime, " +
            "h5_max_sub AS h5MaxSub, api_max_sub AS apiMaxSub, allow_sms AS allowSms, " +
            "channel_default AS channelDefault FROM user_vip WHERE user_id = #{userId}")
    UserVip findByUserId(@Param("userId") long userId);

    @Select("SELECT COUNT(*) FROM alert_rule WHERE user_id = #{userId} AND status = 1")
    int countActiveByUser(@Param("userId") long userId);

    @Insert("INSERT INTO user_vip (user_id, plan_type, expire_time, h5_max_sub, api_max_sub, allow_sms, channel_default) " +
            "VALUES (#{userId}, #{planType}, #{expireTime}, #{h5MaxSub}, #{apiMaxSub}, #{allowSms}, #{channelDefault}) " +
            "ON DUPLICATE KEY UPDATE plan_type = VALUES(plan_type), expire_time = VALUES(expire_time), " +
            "h5_max_sub = VALUES(h5_max_sub), api_max_sub = VALUES(api_max_sub), " +
            "allow_sms = VALUES(allow_sms), channel_default = VALUES(channel_default)")
    int upsert(UserVip v);

    @Update("UPDATE user_vip SET dingtalk_webhook = #{dingtalk}, feishu_webhook = #{feishu}, " +
            "channel_default = #{channelDefault} WHERE user_id = #{userId}")
    int updateWebhooks(@Param("userId") long userId, @Param("dingtalk") String dingtalk,
                       @Param("feishu") String feishu, @Param("channelDefault") String channelDefault);

    /** 过期未降级套餐（expire_time 已过且非 free）——到期降级 Job 用（docs/12 B19 收尾）。 */
    @Select("SELECT user_id FROM user_vip WHERE plan_type != 'free' AND expire_time IS NOT NULL AND expire_time < #{now}")
    java.util.List<Long> findExpired(@Param("now") java.util.Date now);
}
