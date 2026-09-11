#!/usr/bin/env bash
# 数据备份（docs/12 §1.1 #16；docs/07 U0 验收项"数据备份"落地）。
#   MySQL:      mysqldump --single-transaction 一致性快照 + gzip
#   ClickHouse: ALTER TABLE ... FREEZE（本地硬链接快照，落盘在 CK 数据目录 shadow/ 下）
# 恢复演练（每周，docs/08 §3.2）：
#   MySQL: gunzip -c mysql_<db>.sql.gz | mysql -h<host> -P<port> -u<user> -p <db>
#   CK:    freeze 产生的 shadow/bk_<stamp>/ 即分区硬链接，可 rsync 至 <table>/detached/ 后
#          ALTER TABLE <t> ATTACH PARTITION（按 backup_name 元数据恢复）；演练需在本机试跑并记录耗时。
# 用法：MYSQL_PASSWORD=... [BACKUP_DIR=...] [CH_PASSWORD=...] scripts/backup/backup.sh
# 凭据只从环境变量读取，脚本零默认密码字面量。
set -euo pipefail

STAMP="$(date +%Y%m%d_%H%M%S)"
OUT_DIR="${BACKUP_DIR:-./backups/$STAMP}"
mkdir -p "$OUT_DIR"

# ---- MySQL ----
MYSQL_HOST="${MYSQL_HOST:-127.0.0.1}"
MYSQL_PORT="${MYSQL_PORT:-23001}"
MYSQL_USER="${MYSQL_USER:-hqpush}"
MYSQL_PASSWORD="${MYSQL_PASSWORD:?MYSQL_PASSWORD required}"
MYSQL_DB="${MYSQL_DB:-hqpush}"
DUMP_BIN="${DUMP_BIN:-mysqldump}"

echo "[1/2] mysqldump $MYSQL_DB -> $OUT_DIR/mysql_${MYSQL_DB}.sql.gz"
"$DUMP_BIN" -h"$MYSQL_HOST" -P"$MYSQL_PORT" -u"$MYSQL_USER" -p"$MYSQL_PASSWORD" \
  --single-transaction --routines --triggers "$MYSQL_DB" | gzip > "$OUT_DIR/mysql_${MYSQL_DB}.sql.gz"

# ---- ClickHouse ----
CH_URL="${CH_URL:-http://127.0.0.1:23004}"
CH_USER="${CH_USER:-hqpush}"
CH_PASSWORD="${CH_PASSWORD:-}"   # 空则匿名（dev 口径）
CH_TABLES="${CH_TABLES:-tick_local kline_local}"

freeze() {
  local t="$1"
  if [ -n "$CH_PASSWORD" ]; then
    curl -fsS -u "$CH_USER:$CH_PASSWORD" "$CH_URL/" \
      --data-urlencode "query=ALTER TABLE $t FREEZE WITH NAME bk_$STAMP"
  else
    curl -fsS "$CH_URL/" --data-urlencode "query=ALTER TABLE $t FREEZE WITH NAME bk_$STAMP"
  fi
}

echo "[2/2] ClickHouse FREEZE: $CH_TABLES"
for t in $CH_TABLES; do
  freeze "$t" && echo "  frozen: $t (backup_name=bk_$STAMP)"
done

echo "backup done -> $OUT_DIR"
echo "NOTE: CK freeze 快照在 CK 数据目录 shadow/bk_$STAMP/，建议随后 rsync 到备份盘/对象存储。"
