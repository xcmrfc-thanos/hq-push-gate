package com.hqpush.biz.application;

/** U0 简化口令哈希（盐化 SHA-256）。生产替换为 BCrypt/Argon2，见 README 待办。 */
final class BCryptHash {

    private BCryptHash() {}

    static String of(String password) {
        try {
            var md = java.security.MessageDigest.getInstance("SHA-256");
            md.update("hqpush-salt:".getBytes(java.nio.charset.StandardCharsets.UTF_8));
            byte[] d = md.digest(password.getBytes(java.nio.charset.StandardCharsets.UTF_8));
            return java.util.HexFormat.of().formatHex(d);
        } catch (java.security.NoSuchAlgorithmException e) {
            throw new IllegalStateException(e);
        }
    }

    static boolean verify(String password, String hash) {
        return of(password).equals(hash);
    }
}
