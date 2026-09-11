# 构建上下文 = 仓库根（monorepo 单 module）；服务经 CMD_PATH 指定
# docker build -f deploy/docker/go.Dockerfile --build-arg CMD_PATH=services/hq-gateway/cmd/hq-gateway -t hqpush/hq-gateway:0.1.0 .
FROM golang:1.25-alpine AS build
ARG CMD_PATH
WORKDIR /src
COPY . .
# GOPROXY 国内默认（容器内直连 proxy.golang.org 不稳定）；mod 缓存挂载跨构建共享
RUN --mount=type=cache,target=/go/pkg/mod \
    GOPROXY=https://goproxy.cn,direct CGO_ENABLED=0 go build -o /out/app ./${CMD_PATH}

FROM alpine:3.20
RUN adduser -D -u 10001 app
COPY --from=build /out/app /app
USER app
ENTRYPOINT ["/app"]
