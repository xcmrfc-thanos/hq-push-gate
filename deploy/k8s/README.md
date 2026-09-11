# K8s 生产拓扑（M3，docs/06 §3 / docs/07 冻结基线）

## 目录与安装顺序

1. `kubectl apply -k 00-platform/` —— 命名空间、ResourceQuota、PriorityClass（realtime 最高）
2. 前置 Operator（外部安装，见下方清单）
3. `kubectl apply -k 40-stateful/` —— 有状态组件 CR（Kafka/CK/Flink/Redis；MySQL 走云托管）
4. `kubectl apply -k 10-config/ && kubectl apply -k 20-workloads/ && kubectl apply -k 30-jobs/`
5. 边缘：`50-edge/` APISIX Ingress Controller Helm values（外置安装，values 对齐 docs/07 §2 冻结语义）

## 前置 Operator

| 组件 | Operator | 安装 |
|---|---|---|
| Kafka | Strimzi | `kubectl create -f https://strimzi.io/install/latest?namespace=ns-infra` |
| ClickHouse | Altinity | `kubectl apply -f https://github.com/Altinity/clickhouse-operator/raw/main/deploy/operator/install.yaml` |
| Flink | Flink Kubernetes Operator | helm install flink-kubernetes-operator |
| Redis | Redis Operator（OT-Container-Kit） | helm install redis-operator |
| Doris | Doris Operator | U2 启用归档时安装（docs/09） |
| MySQL | 云托管（或 mysql-operator + Orchestrator） | 半同步主从，业务侧仅 DSN |

## 镜像

业务镜像 `ghcr.io/hqpush/<service>:<tag>` 为占位，由 CI 构建推送后替换
（Go 服务：`CGO_ENABLED=0 go build` + distroless；biz-service：Spring Boot 分层 jar）。

## 与开发态（docker-compose）的差异

- Kafka 地址：`hq-kafka-bootstrap.ns-infra:9092`（Strimzi）；RF=3、min.insync.replicas=2
- CK：Altinity 2 shard × 2 replica，恢复 Replicated 引擎 + Distributed 表（migrations/clickhouse 生产版）
- 服务发现走集群内 DNS；宿主机 23001+ 端口规划不适用
- ws-gateway 缩容：SIGTERM → 应用内 drain（DrainGrace）→ preStop 兜底 sleep，
  terminationGracePeriodSeconds 60s（docs/06 §3.3）
