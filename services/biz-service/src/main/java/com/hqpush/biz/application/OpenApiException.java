package com.hqpush.biz.application;

/** 开放面鉴权失败（docs/11 §5/§3：40104 签名无效、40105 时间戳越窗、40106 nonce 重放、42901 限流）。 */
public class OpenApiException extends RuntimeException {
    private final int code;
    private final int httpStatus;
    private final int retryAfter; // Retry-After 响应头秒数（0=不下发；docs/11 §3：429 必带）

    public OpenApiException(int code, int httpStatus, String msg) {
        this(code, httpStatus, msg, 0);
    }

    public OpenApiException(int code, int httpStatus, String msg, int retryAfter) {
        super(msg);
        this.code = code;
        this.httpStatus = httpStatus;
        this.retryAfter = retryAfter;
    }

    public int getCode() { return code; }
    public int getHttpStatus() { return httpStatus; }
    public int getRetryAfter() { return retryAfter; }
}
