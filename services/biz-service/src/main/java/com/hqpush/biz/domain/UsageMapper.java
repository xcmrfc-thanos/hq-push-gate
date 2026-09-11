package com.hqpush.biz.domain;

import org.apache.ibatis.annotations.Insert;
import org.apache.ibatis.annotations.Mapper;
import org.apache.ibatis.annotations.Param;

/** 开放面计量（open_usage_log，V7；docs/12 B18 按 AK 计量）。 */
@Mapper
public interface UsageMapper {

    @Insert("INSERT INTO open_usage_log (access_key, path, status_code, latency_ms) " +
            "VALUES (#{ak}, #{path}, #{statusCode}, #{latencyMs})")
    int insert(@Param("ak") String ak, @Param("path") String path,
               @Param("statusCode") int statusCode, @Param("latencyMs") int latencyMs);
}
