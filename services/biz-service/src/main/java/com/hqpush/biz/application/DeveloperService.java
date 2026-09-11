package com.hqpush.biz.application;

import com.hqpush.biz.domain.DeveloperConfig;
import com.hqpush.biz.domain.DeveloperMapper;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import java.security.SecureRandom;
import java.util.HexFormat;

/** 开发者接入用例（docs/10 §6 / docs/12 B18）：AK 签发、secret 轮换、推送配置。
 *  app_secret 服务端 AES-GCM 加密入库（SecretBox，v1: 格式）；主密钥未配置时降级明文存储
 *  （V4 兼容口径，启动日志 warn），notify 侧两种形态都读。明文 secret 仅在创建/轮换响应出现一次。 */
@Service
public class DeveloperService {

    private static final Logger log = LoggerFactory.getLogger(DeveloperService.class);
    private static final SecureRandom RNG = new SecureRandom();

    private final DeveloperMapper devs;

    public DeveloperService(DeveloperMapper devs) {
        this.devs = devs;
    }

    /** 创建/轮换结果：cfg.appSecret 为密文（不入响应）；plainSecret 仅供本次响应一次性下发。 */
    public record Issued(DeveloperConfig cfg, String plainSecret) {
    }

    public DeveloperConfig getByUser(long userId) {
        return devs.findByUser(userId);
    }

    public DeveloperConfig getByAccessKey(String accessKey) {
        return devs.findByAccessKey(accessKey);
    }

    @Transactional
    public Issued create(long userId, String pushMode, String hookUrl) {
        if (devs.findByUser(userId) != null) {
            throw new RuleService.BadRequestException("developer config exists (use rotate)");
        }
        return doIssue(userId, pushMode, hookUrl, false);
    }

    @Transactional
    public Issued rotate(long userId) {
        DeveloperConfig cur = devs.findByUser(userId);
        if (cur == null) {
            throw new RuleService.NotFoundException("developer config not found");
        }
        String plain = newAppSecret();
        cur.setAppSecret(storeSecret(plain));
        devs.updateSecret(userId, cur.getAppSecret());
        return new Issued(cur, plain);
    }

    @Transactional
    public void updatePush(long userId, String pushMode, String hookUrl) {
        DeveloperConfig cur = devs.findByUser(userId);
        if (cur == null) {
            throw new RuleService.NotFoundException("developer config not found");
        }
        if (!"ws".equals(pushMode) && !"webhook".equals(pushMode)) {
            throw new RuleService.BadRequestException("push_mode must be ws|webhook");
        }
        if ("webhook".equals(pushMode) && (hookUrl == null || !hookUrl.startsWith("https://"))) {
            throw new RuleService.BadRequestException("webhook push_mode requires https hook_url");
        }
        devs.updatePush(userId, pushMode, hookUrl == null ? "" : hookUrl);
    }

    private Issued doIssue(long userId, String pushMode, String hookUrl, boolean rotate) {
        String plain = newAppSecret();
        DeveloperConfig c = new DeveloperConfig();
        c.setUserId(userId);
        c.setAccessKey("ak_" + randomHex(12));
        c.setAppSecret(storeSecret(plain));
        c.setPushMode(pushMode == null ? "ws" : pushMode);
        c.setHookUrl(hookUrl == null ? "" : hookUrl);
        devs.insert(c);
        return new Issued(c, plain);
    }

    private String storeSecret(String plain) {
        try {
            return SecretBox.encrypt(plain);
        } catch (IllegalStateException e) {
            log.warn("HQ_MASTER_KEYS not configured; app_secret stored plaintext (legacy V4 口径)");
            return plain;
        }
    }

    private static String newAppSecret() {
        return "sk_" + randomHex(32);
    }

    private static String randomHex(int bytes) {
        byte[] b = new byte[bytes];
        RNG.nextBytes(b);
        return HexFormat.of().formatHex(b);
    }
}
