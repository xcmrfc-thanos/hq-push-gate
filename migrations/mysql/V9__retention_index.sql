-- V9 数据生命周期清理索引（docs/12 §1.1 #19；清理 Job 依赖，U2 数据量起来前补齐）：
-- outbox_event 仅删 PUBLISHED 且超过保留期的行（status, published_at 走联合索引）；
-- notify_send_log 按 created_at 批量删除。保留期默认：outbox 7d / send_log 90d（可 env 调整）。

ALTER TABLE outbox_event
  ADD KEY idx_cleanup (status, published_at);

ALTER TABLE notify_send_log
  ADD KEY idx_cleanup (created_at);
