#!/usr/bin/env bash
# Topic 初始化（U0 开发分区数；生产分区/副本按 docs/02、docs/06 调整，Topic 名为冻结项）
# 用法: scripts/topic-init.sh [bootstrap-servers]
set -euo pipefail

BOOTSTRAP="${1:-localhost:9092}"

create() {
  local topic="$1" partitions="$2" config="${3:-}"
  if kafka-topics.sh --bootstrap-server "$BOOTSTRAP" --list 2>/dev/null | grep -qx "$topic"; then
    echo "topic $topic exists, skip"
  else
    local args=(--create --if-not-exists --topic "$topic" --partitions "$partitions" --replication-factor 1)
    [ -n "$config" ] && args+=(--config "$config")
    kafka-topics.sh --bootstrap-server "$BOOTSTRAP" "${args[@]}"
  fi
}

#                    topic            分区            配置
create tick_raw       24   "retention.ms=259200000"      # 3d
create rule_bcast      1   "retention.ms=259200000"      # 广播语义单分区
create alert_event    12   "retention.ms=259200000"      # 3d，与告警补拉窗口对齐
create snapshot_kline 12   "retention.ms=259200000"
create ws_push        64   "retention.ms=3600000"        # 1h，固定槽位分区
create notify_retry    6   "retention.ms=259200000"
create dead_letter     3   "retention.ms=604800000"      # 7d

echo "topics ready at $BOOTSTRAP"
