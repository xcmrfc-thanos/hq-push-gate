#!/usr/bin/env bash
# 迁移治理（docs/12 §1.1 #17）：顺序应用 migrations/mysql/V*.sql 并登记 schema_migrations，幂等可重复执行。
# compose 首启经 docker-entrypoint-initdb.d 建库后，其后的增量迁移统一用本脚本（替代手工 mysql < V*.sql）。
# 用法：MYSQL_PASSWORD=... [MYSQL_HOST=...] [MYSQL_PORT=...] [MYSQL_USER=...] [MYSQL_DB=...] scripts/migrate/apply.sh [--base N]
#   --base N：V1~VN 已由 initdb/旧数据卷建立，仅登记不执行（旧卷共存场景，N 取最后已应用版本号）。
# 凭据只从环境变量读取，脚本零默认密码字面量。
set -euo pipefail

MYSQL_HOST="${MYSQL_HOST:-127.0.0.1}"
MYSQL_PORT="${MYSQL_PORT:-23001}"
MYSQL_USER="${MYSQL_USER:-hqpush}"
MYSQL_PASSWORD="${MYSQL_PASSWORD:?MYSQL_PASSWORD required}"
MYSQL_DB="${MYSQL_DB:-hqpush}"
MYSQL_BIN="${MYSQL_BIN:-mysql}"
DIR="$(cd "$(dirname "$0")/../.." && pwd)/migrations/mysql"

BASE=0
while [ $# -gt 0 ]; do
  case "$1" in
    --base) BASE="${2:-0}"; shift 2 ;;
    *) shift ;;
  esac
done

# docker exec 必须带 -i 才能把 stdin（SQL 文件）传进容器；无 -i 时 mysql 读到空输入
# 会 exit 0 但一行 SQL 都不执行（静默假成功）——自动补全。
case "$MYSQL_BIN" in
  docker\ exec*) MYSQL_BIN="${MYSQL_BIN/docker exec/docker exec -i}" ;;
esac

# MYSQL_BIN 支持含空格包装（如 "docker exec <cid> mysql"），去引号按词拆分
m() { $MYSQL_BIN -h"$MYSQL_HOST" -P"$MYSQL_PORT" -u"$MYSQL_USER" -p"$MYSQL_PASSWORD" "$MYSQL_DB" "$@"; }

m -N -e "CREATE TABLE IF NOT EXISTS schema_migrations (
  version VARCHAR(64) PRIMARY KEY,
  applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
)"

# vnum 从 V{n}__slug.sql 提取版本号；非标准命名（如 V8b__）解析失败回退 -1（不参与 base-skip，必须执行）
vnum() { n="$(echo "$1" | sed -E 's/^V([0-9]+)__.*/\1/')"; case "$n" in ''|*[!0-9]*) echo -1 ;; *) echo "$n" ;; esac; }

applied=0 skipped=0
for f in "$DIR"/V*.sql; do
  [ -e "$f" ] || { echo "no migration files under $DIR"; exit 0; }
  name="$(basename "$f")"
  if [ "$(m -N -e "SELECT COUNT(*) FROM schema_migrations WHERE version='$name'")" != "0" ]; then
    skipped=$((skipped + 1))
    continue
  fi
  n="$(vnum "$name")"
  if [ "$BASE" -gt 0 ] && [ "$n" -ge 0 ] && [ "$n" -le "$BASE" ]; then
    # 旧卷/initdb 已建立：仅登记，不执行
    m -e "INSERT INTO schema_migrations(version) VALUES('$name')"
    echo "skip(base<=$BASE) $name"
    skipped=$((skipped + 1))
    continue
  fi
  echo "applying $name"
  m < "$f"
  m -e "INSERT INTO schema_migrations(version) VALUES('$name')"
  applied=$((applied + 1))
done
echo "migrate done: applied=$applied skipped=$skipped"
