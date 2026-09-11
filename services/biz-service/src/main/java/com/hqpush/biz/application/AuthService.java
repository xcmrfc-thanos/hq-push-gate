package com.hqpush.biz.application;

import com.hqpush.biz.domain.User;
import com.hqpush.biz.domain.UserMapper;
import org.springframework.stereotype.Service;

import java.util.Map;

/** 注册/登录/换发用例（POST /auth/register、/auth/login、/auth/refresh）。 */
@Service
public class AuthService {

    private final UserMapper users;
    private final TokenService tokens;

    public AuthService(UserMapper users, TokenService tokens) {
        this.users = users;
        this.tokens = tokens;
    }

    public Map<String, Object> register(String username, String password) {
        if (username == null || username.isBlank() || password == null || password.length() < 6) {
            throw new RuleService.BadRequestException("invalid username or password");
        }
        if (users.findByUsername(username) != null) {
            throw new RuleService.BadRequestException("username exists");
        }
        User u = new User();
        u.setId(users.maxId() + 1);
        u.setUsername(username);
        u.setPasswordHash(BCryptHash.of(password)); // U0 简化哈希；生产用 BCrypt/Argon2 并迁移
        users.insert(u);
        return session(u.getId());
    }

    public Map<String, Object> login(String username, String password) {
        User u = users.findByUsername(username);
        if (u == null || !BCryptHash.verify(password, u.getPasswordHash())) {
            throw new UnauthorizedException("bad credentials");
        }
        return session(u.getId());
    }

    public Map<String, Object> refresh(String refreshToken) {
        long uid;
        try {
            uid = tokens.verify(refreshToken, "refresh");
        } catch (Exception e) {
            throw new UnauthorizedException("invalid refresh token");
        }
        return session(uid);
    }

    private Map<String, Object> session(long uid) {
        return Map.of(
                "user_id", uid,
                "access_token", tokens.issueAccess(uid),
                "refresh_token", tokens.issueRefresh(uid)
        );
    }

    public static class UnauthorizedException extends RuntimeException {
        public UnauthorizedException(String msg) { super(msg); }
    }
}
