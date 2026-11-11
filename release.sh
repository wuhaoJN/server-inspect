#!/usr/bin/env sh
# ============================================================
# 构建 Linux 发布包（amd64 / arm64）
# 产物输出到 dist/，包含二进制、安装脚本、systemd 单元与校验和
# ============================================================
set -eu

VERSION="${VERSION:-1.0.0}"
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
DIST="$ROOT/dist"

cd "$ROOT"
rm -rf "$DIST"
mkdir -p "$DIST"

build() {
    arch="$1"
    echo "==> 构建 linux/${arch} ..."
    CGO_ENABLED=0 GOOS=linux GOARCH="$arch" \
        go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
        -o "$DIST/server-inspect-linux-${arch}" .
}

build amd64
build arm64

cp deploy/install.sh "$DIST/install.sh"
cp deploy/server-inspect.service "$DIST/server-inspect.service"
cp README.md DEVELOPMENT.md ROADMAP.md AGENTS.md "$DIST/"
chmod +x "$DIST/install.sh" "$DIST/server-inspect-linux-amd64" "$DIST/server-inspect-linux-arm64"

if command -v sha256sum >/dev/null 2>&1; then
    (cd "$DIST" && sha256sum server-inspect-linux-* install.sh >SHA256SUMS)
elif command -v shasum >/dev/null 2>&1; then
    (cd "$DIST" && shasum -a 256 server-inspect-linux-* install.sh >SHA256SUMS)
fi

echo ""
echo "==> 发布包已生成：$DIST"
ls -lh "$DIST"
echo ""
echo "上传到服务器："
echo "  scp dist/server-inspect-linux-amd64 dist/install.sh user@server:/tmp/"
echo "  ssh user@server 'sudo bash /tmp/install.sh --port 8080'"
