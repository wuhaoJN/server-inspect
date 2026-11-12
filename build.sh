#!/usr/bin/env sh
# 构建 Linux/macOS 可执行文件（纯 Go，无 CGO）
set -eu

VERSION="${VERSION:-1.0.13}"
OUTPUT="${OUTPUT:-server-inspect}"

echo "==> 正在构建 ${OUTPUT} (version ${VERSION}) ..."
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o "${OUTPUT}" .
ls -lh "${OUTPUT}"
echo "==> 运行: ./${OUTPUT} -addr :8080 -data ./data"
