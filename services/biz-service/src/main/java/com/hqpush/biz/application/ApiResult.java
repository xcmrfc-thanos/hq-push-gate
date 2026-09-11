package com.hqpush.biz.application;

import java.util.Collections;
import java.util.Map;

/** 统一响应信封载体（docs/11 §2.2，U1 收尾补做——替换各 Controller 手写 Map.of）。
 *  成功 {code:0, msg:"ok", data}；失败 {code, msg}（data 省略）。data 为 null 时用空 Map，
 *  规避 Map.of 不允许 null value 的 NPE（docs/11 §2.2 注）。 */
public final class ApiResult {

    private ApiResult() {
    }

    public static Map<String, Object> ok(Object data) {
        return Map.of("code", 0, "msg", "ok",
                "data", data == null ? Collections.emptyMap() : data);
    }

    public static Map<String, Object> ok() {
        return ok(null);
    }

    public static Map<String, Object> err(int code, String msg) {
        return Map.of("code", code, "msg", msg);
    }
}
