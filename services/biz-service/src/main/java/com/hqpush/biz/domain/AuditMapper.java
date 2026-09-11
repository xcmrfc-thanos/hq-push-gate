package com.hqpush.biz.domain;

import org.apache.ibatis.annotations.Insert;
import org.apache.ibatis.annotations.Mapper;
import org.apache.ibatis.annotations.Param;

/** 管理操作审计（admin_audit_log，V8；docs/12 §1.1 #20——管理变更留痕）。 */
@Mapper
public interface AuditMapper {

    @Insert("INSERT INTO admin_audit_log (operator, action, target_type, target_id, detail) " +
            "VALUES (#{operator}, #{action}, #{targetType}, #{targetId}, #{detail})")
    int insert(@Param("operator") String operator, @Param("action") String action,
               @Param("targetType") String targetType, @Param("targetId") String targetId,
               @Param("detail") String detail);
}
