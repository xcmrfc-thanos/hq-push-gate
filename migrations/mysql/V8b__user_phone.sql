ALTER TABLE `user`
  ADD COLUMN phone VARCHAR(20) NULL COMMENT '手机号（短信收件人；V3 邮件已有，B19 补 phone）' AFTER email,
  ADD UNIQUE KEY uk_phone (phone);
