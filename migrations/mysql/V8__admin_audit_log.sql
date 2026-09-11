-- 商业化闭环（Branch 19，docs/12 §2.2 / §1.1 #20）：管理操作审计 + 用户手机号
CREATE TABLE admin_audit_log (
  id          BIGINT PRIMARY KEY AUTO_INCREMENT,
  operator    VARCHAR(64) NOT NULL,          -- 管理端操作人/内部令牌标识
  action      VARCHAR(64) NOT NULL,          -- grant_plan|renew_plan|rotate_secret|ban_user|...
  target_type VARCHAR(32) NOT NULL,          -- user|developer|channel
  target_id   VARCHAR(64) NOT NULL,
  detail      VARCHAR(512) NULL,             -- 变更摘要（脱敏，禁明文凭据）
  created_at  DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
  KEY idx_target (target_type, target_id),
  KEY idx_created (created_at)
);
