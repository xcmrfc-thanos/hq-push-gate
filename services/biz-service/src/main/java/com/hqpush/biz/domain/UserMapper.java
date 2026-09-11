package com.hqpush.biz.domain;

import org.apache.ibatis.annotations.*;

/** 用户 Mapper（user 表，docs/04 §3）。 */
@Mapper
public interface UserMapper {

    @Select("SELECT id, username, password_hash AS passwordHash, role, status FROM `user` WHERE username = #{username}")
    User findByUsername(@Param("username") String username);

    @Select("SELECT id, username, password_hash AS passwordHash, role, status FROM `user` WHERE id = #{id}")
    User findById(@Param("id") long id);

    @Insert("INSERT INTO `user` (id, username, password_hash, role) " +
            "VALUES (#{id}, #{username}, #{passwordHash}, 'USER')")
    @Options(useGeneratedKeys = false)
    int insert(User user);

    @Select("SELECT IFNULL(MAX(id),0) FROM `user`")
    long maxId();

    @Update("UPDATE `user` SET email = #{email} WHERE id = #{userId}")
    int updateEmail(@Param("userId") long userId, @Param("email") String email);

    @Update("UPDATE `user` SET phone = #{phone} WHERE id = #{userId}")
    int updatePhone(@Param("userId") long userId, @Param("phone") String phone);
}
