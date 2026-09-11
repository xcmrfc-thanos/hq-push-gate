package com.hqpush.biz.domain;

import org.apache.ibatis.annotations.*;

/** 开发者接入配置（api_developer_config，V4；docs/10 §6 / docs/12 B18）。 */
@Mapper
public interface DeveloperMapper {

    @Select("SELECT user_id AS userId, access_key AS accessKey, app_secret AS appSecret, " +
            "push_mode AS pushMode, IFNULL(hook_url,'') AS hookUrl, push_subscribe_changed AS pushSubscribeChanged " +
            "FROM api_developer_config WHERE user_id = #{userId}")
    DeveloperConfig findByUser(@Param("userId") long userId);

    @Select("SELECT user_id AS userId, access_key AS accessKey, app_secret AS appSecret, " +
            "push_mode AS pushMode, IFNULL(hook_url,'') AS hookUrl, push_subscribe_changed AS pushSubscribeChanged " +
            "FROM api_developer_config WHERE access_key = #{accessKey}")
    DeveloperConfig findByAccessKey(@Param("accessKey") String accessKey);

    @Insert("INSERT INTO api_developer_config (user_id, access_key, app_secret, push_mode, hook_url, push_subscribe_changed) " +
            "VALUES (#{userId}, #{accessKey}, #{appSecret}, #{pushMode}, #{hookUrl}, 0)")
    int insert(DeveloperConfig c);

    @Update("UPDATE api_developer_config SET app_secret = #{appSecret} WHERE user_id = #{userId}")
    int updateSecret(@Param("userId") long userId, @Param("appSecret") String appSecret);

    @Update("UPDATE api_developer_config SET push_mode = #{pushMode}, hook_url = #{hookUrl} " +
            "WHERE user_id = #{userId}")
    int updatePush(@Param("userId") long userId, @Param("pushMode") String pushMode,
                   @Param("hookUrl") String hookUrl);
}
