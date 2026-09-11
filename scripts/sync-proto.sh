#!/usr/bin/env bash
# 同步公共契约到各消费模块（修改 proto/ 后执行；Java 模块需要 src/main/proto 副本）
set -euo pipefail
cd "$(dirname "$0")/.."

cp proto/hq/v1/*.proto flink/src/main/proto/hq/v1/
echo "protos synced to flink/src/main/proto"
