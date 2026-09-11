package com.hqpush.biz.interfaces;

import com.hqpush.biz.application.AuthService;
import com.hqpush.biz.application.ApiResult;

import com.hqpush.biz.application.RuleService;
import com.hqpush.biz.application.SubscribeService;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.http.HttpStatus;
import org.springframework.web.bind.annotation.ExceptionHandler;
import org.springframework.web.bind.annotation.ResponseStatus;
import org.springframework.web.bind.annotation.RestControllerAdvice;

import java.util.Map;

/** 统一响应与错误码（docs/04 §6 + docs/11 §3 注册表：{code,msg,data}，错误响应必须带正确 HTTP status）。 */
@RestControllerAdvice
public class GlobalExceptionHandler {

    private static final Logger log = LoggerFactory.getLogger(GlobalExceptionHandler.class);

    @ExceptionHandler({RuleService.BadRequestException.class, SubscribeService.BadRequestException.class,
            IllegalArgumentException.class})
    @ResponseStatus(HttpStatus.BAD_REQUEST)
    public Map<String, Object> badRequest(Exception e) {
        return ApiResult.err(40010, e.getMessage());
    }

    @ExceptionHandler({AuthService.UnauthorizedException.class, java.security.GeneralSecurityException.class})
    @ResponseStatus(HttpStatus.UNAUTHORIZED)
    public Map<String, Object> unauthorized(Exception e) {
        return ApiResult.err(40101, e.getMessage());
    }

    /** 超出套餐订阅配额（docs/11 §3 注册表 42910，HTTP 429；原误入 503 兜底）。
     *  配额为非周期重置的硬上限，Retry-After 给保守小时级退避（客户端可升级/退订后重试）。 */
    @ExceptionHandler({RuleService.QuotaExceededException.class, SubscribeService.QuotaExceededException.class})
    @ResponseStatus(HttpStatus.TOO_MANY_REQUESTS)
    public Map<String, Object> quotaExceeded(Exception e, jakarta.servlet.http.HttpServletResponse resp) {
        resp.setHeader("Retry-After", "3600");
        return ApiResult.err(42910, e.getMessage());
    }

    /** 套餐不含该渠道权限（如免费用户要短信；docs/11 §3 注册表 40310，HTTP 403）。 */
    @ExceptionHandler(SubscribeService.SmsNotAllowedException.class)
    @ResponseStatus(HttpStatus.FORBIDDEN)
    public Map<String, Object> smsNotAllowed(Exception e) {
        return ApiResult.err(40310, e.getMessage());
    }

    @ExceptionHandler(RuleService.NotFoundException.class)
    @ResponseStatus(HttpStatus.NOT_FOUND)
    public Map<String, Object> notFound(Exception e) {
        return ApiResult.err(40401, e.getMessage());
    }

    @ExceptionHandler(Exception.class)
    @ResponseStatus(HttpStatus.INTERNAL_SERVER_ERROR)
    public Map<String, Object> internal(Exception e) {
        log.error("unhandled error", e);
        return ApiResult.err(50000, "internal error");
    }
}
