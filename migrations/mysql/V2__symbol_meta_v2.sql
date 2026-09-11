-- symbol_meta V2（docs/09 §5.1 Branch 4）：静态属性扩展；动态行情仍留 Redis（docs/07 §5 冻结）。
-- 回填来源：scripts/backfill/sync_symbol_meta.py（东财 clist，全市场 A 股）

ALTER TABLE symbol_meta
  ADD COLUMN board      VARCHAR(64) NULL COMMENT '所属板块',
  ADD COLUMN list_date  DATE NULL COMMENT '上市日期',
  ADD COLUMN status     TINYINT DEFAULT 1 COMMENT '1上市 0退市',
  ADD COLUMN updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP;
