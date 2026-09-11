package com.hqpush.biz.application;

import javax.crypto.Cipher;
import javax.crypto.spec.GCMParameterSpec;
import javax.crypto.spec.SecretKeySpec;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.SecureRandom;
import java.util.Base64;
import java.util.LinkedHashMap;
import java.util.Map;

/** 渠道凭据加密（docs/11 §7.2；与 packages/secretx(Go) / ai-query secrets.py 同格式同口径）。
 *  密文格式 v1:&lt;kid&gt;:&lt;base64url(nonce||ct||tag)&gt;，AES-256-GCM，AAD 绑定 kid。
 *  主密钥只从环境变量读取：HQ_MASTER_KEYS="k1=&lt;hex64&gt;[,k0=...]"（首个为加密钥），兼容 HQ_MASTER_KEY。 */
public final class SecretBox {

    private static final String PREFIX = "v1";
    private static final int NONCE_LEN = 12;
    private static final int TAG_BITS = 128;
    private static final SecureRandom RNG = new SecureRandom();

    private SecretBox() {
    }

    /** 解析主密钥环：kid -> key（保持插入序，首个为加密钥）。 */
    public static Map<String, byte[]> loadKeys() {
        String raw = env("HQ_MASTER_KEYS");
        if (raw.isBlank()) {
            String legacy = env("HQ_MASTER_KEY");
            raw = legacy.isBlank() ? "" : "k1=" + legacy;
        }
        if (raw.isBlank()) {
            throw new IllegalStateException("HQ_MASTER_KEYS/HQ_MASTER_KEY not configured");
        }
        Map<String, byte[]> keys = new LinkedHashMap<>();
        for (String part : raw.split(",")) {
            String p = part.trim();
            if (p.isEmpty()) {
                continue;
            }
            String kid = "k1";
            String hex = p;
            int eq = p.indexOf('=');
            if (eq > 0) {
                kid = p.substring(0, eq).trim();
                hex = p.substring(eq + 1).trim();
            }
            byte[] key = hexDecode(hex);
            if (key.length != 32) {
                throw new IllegalStateException("master key " + kid + " must be 32-byte hex64");
            }
            keys.putIfAbsent(kid, key);
        }
        if (keys.isEmpty()) {
            throw new IllegalStateException("no master key parsed");
        }
        return keys;
    }

    public static String encrypt(String plaintext) {
        Map.Entry<String, byte[]> first = loadKeys().entrySet().iterator().next();
        return encryptWith(plaintext, first.getKey(), first.getValue());
    }

    public static String encryptWith(String plaintext, String kid, byte[] key) {
        try {
            byte[] nonce = new byte[NONCE_LEN];
            RNG.nextBytes(nonce);
            Cipher c = Cipher.getInstance("AES/GCM/NoPadding");
            c.init(Cipher.ENCRYPT_MODE, new SecretKeySpec(key, "AES"), new GCMParameterSpec(TAG_BITS, nonce));
            c.updateAAD(kid.getBytes(StandardCharsets.UTF_8));
            byte[] ct = c.doFinal(plaintext.getBytes(StandardCharsets.UTF_8));
            byte[] blob = new byte[nonce.length + ct.length];
            System.arraycopy(nonce, 0, blob, 0, nonce.length);
            System.arraycopy(ct, 0, blob, nonce.length, ct.length);
            return PREFIX + ":" + kid + ":" + Base64.getUrlEncoder().withoutPadding().encodeToString(blob);
        } catch (Exception e) {
            throw new IllegalStateException("secretbox encrypt failed", e);
        }
    }

    public static String decrypt(String token) {
        String[] parts = token.split(":", 3);
        if (parts.length != 3 || !PREFIX.equals(parts[0])) {
            throw new IllegalArgumentException("bad token format");
        }
        byte[] key = loadKeys().get(parts[1]);
        if (key == null) {
            throw new IllegalArgumentException("unknown kid " + parts[1]);
        }
        try {
            byte[] blob = Base64.getUrlDecoder().decode(parts[2]);
            Cipher c = Cipher.getInstance("AES/GCM/NoPadding");
            c.init(Cipher.DECRYPT_MODE, new SecretKeySpec(key, "AES"),
                    new GCMParameterSpec(TAG_BITS, blob, 0, NONCE_LEN));
            c.updateAAD(parts[1].getBytes(StandardCharsets.UTF_8));
            byte[] pt = c.doFinal(blob, NONCE_LEN, blob.length - NONCE_LEN);
            return new String(pt, StandardCharsets.UTF_8);
        } catch (Exception e) {
            throw new IllegalArgumentException("secretbox decrypt failed");
        }
    }

    /** 日志/列表脱敏：abc***last4（docs/11 §7.2）。 */
    public static String mask(String s) {
        if (s == null || s.length() <= 8) {
            return "***";
        }
        return s.substring(0, 3) + "***" + s.substring(s.length() - 4);
    }

    private static String env(String k) {
        String v = System.getenv(k);
        return v == null ? "" : v.trim();
    }

    private static byte[] hexDecode(String s) {
        if (s.length() % 2 != 0) {
            throw new IllegalStateException("odd hex length");
        }
        byte[] out = new byte[s.length() / 2];
        for (int i = 0; i < out.length; i++) {
            out[i] = (byte) Integer.parseInt(s.substring(i * 2, i * 2 + 2), 16);
        }
        return out;
    }

    /** hex(MessageDigest) 工具（开放面签名串 SHA256 用）。 */
    public static String sha256Hex(byte[] data) {
        try {
            byte[] d = MessageDigest.getInstance("SHA-256").digest(data);
            StringBuilder sb = new StringBuilder(d.length * 2);
            for (byte b : d) {
                sb.append(Character.forDigit((b >> 4) & 0xF, 16)).append(Character.forDigit(b & 0xF, 16));
            }
            return sb.toString();
        } catch (Exception e) {
            throw new IllegalStateException(e);
        }
    }
}
