package com.hqpush.biz.interfaces;

import com.hqpush.biz.application.AuthService;
import com.hqpush.biz.application.ApiResult;

import com.hqpush.biz.application.TokenService;
import com.hqpush.biz.domain.UserMapper;
import org.springframework.http.HttpStatus;
import org.springframework.web.bind.annotation.*;

import java.util.Map;

/** 鉴权入口（POST /api/v1/auth/*，docs/04 §6）。 */
@RestController
@RequestMapping("/api/v1/auth")
public class AuthController {

    private final AuthService auth;
    private final UserMapper users;
    private final TokenService tokens;

    public AuthController(AuthService auth, UserMapper users, TokenService tokens) {
        this.auth = auth;
        this.users = users;
        this.tokens = tokens;
    }

    @PostMapping("/register")
    public Map<String, Object> register(@RequestBody Map<String, String> body) {
        return ok(auth.register(body.get("username"), body.get("password")));
    }

    @PostMapping("/login")
    public Map<String, Object> login(@RequestBody Map<String, String> body) {
        return ok(auth.login(body.get("username"), body.get("password")));
    }

    @PostMapping("/refresh")
    public Map<String, Object> refresh(@RequestBody Map<String, String> body) {
        return ok(auth.refresh(body.get("refresh_token")));
    }

    /** 演示辅助：仅用于联调生成 WS token；生产环境不允许。 */
    @GetMapping("/ws-token")
    public Map<String, Object> wsToken(@RequestHeader("Authorization") String authorization) {
        long uid = currentUid(authorization);
        return ok(Map.of("ws_token", tokens.issueAccess(uid)));
    }

    long currentUid(String authorization) {
        if (authorization == null || !authorization.startsWith("Bearer ")) {
            throw new AuthService.UnauthorizedException("missing bearer token");
        }
        try {
            return tokens.verify(authorization.substring(7), "access");
        } catch (Exception e) {
            throw new AuthService.UnauthorizedException("invalid token");
        }
    }

    static Map<String, Object> ok(Map<String, Object> data) {
        return ApiResult.ok(data);
    }

    @ResponseStatus(HttpStatus.UNAUTHORIZED)
    static class Unauthorized extends RuntimeException {}
}
