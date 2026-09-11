# hq-push-gate 跨服务统一入口（docs/08 §5.1）

SHELL := /bin/bash
GO := go
BOOTSTRAP ?= localhost:9092

.PHONY: proto-gen proto-lint build biz-build flink-build flink-submit web-install web-build web-dev-pc web-dev-h5 vet test check up down ps run-hq-gateway run-quote-push run-notify run-ws-gateway run-ingest-worker run-outbox-publisher run-tick-source bench-ws

## 代码生成：.proto -> Go（buf + protoc-gen-go，无需 protoc）
proto-gen:
	export PATH=$$PATH:$$($(GO) env GOPATH)/bin; buf generate proto

proto-lint:
	export PATH=$$PATH:$$($(GO) env GOPATH)/bin; buf lint proto

## 构建全部 Go 服务
build:
	$(GO) build ./...

## 构建 Java 模块（需 JDK 21：JAVA_HOME 指向 jdk-21）
biz-build:
	cd services/biz-service && mvn -q -DskipTests package

flink-build:
	cd flink && mvn -q -DskipTests package

flink-submit: flink-build
	docker compose -f deploy/docker-compose.yml exec flink-jobmanager \
		/opt/flink/bin/flink run -d -c com.hqpush.flink.job.TickRuleJob /tmp/flink-rules.jar || true
	@echo "提示：先将 flink/target/flink-rules-1.0.0.jar 拷入 jobmanager 容器（docker cp）"

## 前端（npm workspaces：packages + apps）
web-install:
	npm install --no-audit --no-fund

web-build: web-install
	npm run build

web-dev-pc:
	npm run dev --workspace apps/pc-web
web-dev-h5:
	npm run dev --workspace apps/h5

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

## 本地提交前最低门槛：契约 lint + Go vet/test（Java/Python/Web 全量门禁在 CI）
check: proto-lint vet test

## U0 开发环境（MySQL/Redis/Kafka/CK；flink、obs 用 profile 开启）
up:
	docker compose -f deploy/docker-compose.yml up -d
down:
	docker compose -f deploy/docker-compose.yml down
ps:
	docker compose -f deploy/docker-compose.yml ps

## 本地运行各服务
run-hq-gateway:
	$(GO) run ./services/hq-gateway/cmd/hq-gateway
run-quote-push:
	$(GO) run ./services/quote-push/cmd/quote-push
run-notify:
	$(GO) run ./services/notify/cmd/notify
run-ws-gateway:
	$(GO) run ./services/ws-gateway/cmd/ws-gateway
run-ingest-worker:
	$(GO) run ./services/ingest-worker/cmd/ingest-worker
run-outbox-publisher:
	$(GO) run ./services/outbox-publisher/cmd/outbox-publisher

## 压测
run-tick-source:
	$(GO) run ./bench/tick-source/cmd/tick-source
bench-ws:
	$(GO) run ./bench/ws-bench/cmd/ws-bench
