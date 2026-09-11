#!/usr/bin/env bash
# 生成开发用自签证书（APISIX TLS 终止演示）；生产环境使用托管/CA 证书，禁止提交私钥。
# 注意：APISIX standalone 的 ssls.cert/key 只接受 PEM 内容（不支持文件路径），
# 重新生成后需将 dev.crt/dev.key 内容同步进 deploy/apisix/apisix.yaml 的 ssls 段。
set -euo pipefail
cd "$(dirname "$0")/../deploy/apisix/certs"

# Git Bash/MSYS 会把 "/CN=..." 当路径转换为盘符路径，用双斜杠规避
SUBJ="/CN=localhost"
case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*) SUBJ="//CN=localhost" ;;
esac

mkdir -p .
openssl req -x509 -newkey rsa:2048 -nodes -days 365 \
  -keyout dev.key -out dev.crt \
  -subj "$SUBJ" \
  -addext "subjectAltName=DNS:localhost,IP:127.0.0.1"

echo "certs written: $(pwd)/dev.crt dev.key"
