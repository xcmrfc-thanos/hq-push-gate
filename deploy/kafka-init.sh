#!/usr/bin/env bash
# Kafka Topic 初始化（容器内执行；分区数为冻结契约，docs/04 §2）
set -euo pipefail

BOOTSTRAP="${BOOTSTRAP:-localhost:9092}"
KAFKA_TOPICS=/opt/kafka/bin/kafka-topics.sh

until "$KAFKA_TOPICS" --bootstrap-server "$BOOTSTRAP" --list >/dev/null 2>&1; do
  echo "waiting for kafka at $BOOTSTRAP..."
  sleep 3
done

create() {
  local topic="$1" partitions="$2" retention="$3"
  if "$KAFKA_TOPICS" --bootstrap-server "$BOOTSTRAP" --list | grep -qx "$topic"; then
    echo "topic $topic exists, skip"
  else
    "$KAFKA_TOPICS" --bootstrap-server "$BOOTSTRAP" \
      --create --if-not-exists --topic "$topic" \
      --partitions "$partitions" --replication-factor 1 \
      --config retention.ms="$retention"
  fi
}

create tick_raw       24 259200000   # 3d
create rule_bcast      1 259200000   # 广播语义单分区
create alert_event    12 259200000   # 3d，与告警补拉窗口对齐
create snapshot_kline 12 259200000
create ws_push        64   3600000   # 1h，固定槽位分区
create notify_retry    6 259200000
create dead_letter     3 604800000   # 7d

# 内部 topic：auto-create 关闭后必须显式创建，否则消费组加入报 Coordinator Not Available
"$KAFKA_TOPICS" --bootstrap-server "$BOOTSTRAP" \
  --create --if-not-exists --topic __consumer_offsets \
  --partitions 50 --replication-factor 1 --config cleanup.policy=compact

echo "topics ready"
