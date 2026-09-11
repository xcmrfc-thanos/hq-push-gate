package com.hqpush.biz.domain;

import org.apache.ibatis.annotations.Mapper;
import org.apache.ibatis.annotations.Param;
import org.apache.ibatis.annotations.Select;

/** 标的元数据只读查询（symbol_meta，docs/10 §3.2 第 6 步订阅存在性校验，Step1 D10-1）。 */
@Mapper
public interface SymbolMetaMapper {

    @Select("SELECT COUNT(*) FROM symbol_meta WHERE market = #{market} AND symbol = #{symbol} AND status = 1")
    int countListed(@Param("market") String market, @Param("symbol") String symbol);
}
