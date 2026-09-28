#!/usr/bin/env bash
#
# 交叉编译 Linux 离线安装包（amd64 / arm64），供 Debian / Ubuntu / CentOS 原生部署
#
#   bash deploy/build.sh                        # 版本号自动取 internal/config/config.go
#   bash deploy/build.sh 0.3.3                  # 指定版本号
#   bash deploy/build.sh 0.3.3 "amd64 arm64"    # 指定架构
#   SKIP_WEB=1 bash deploy/build.sh             # 复用已构建的 web/dist（无 npm 的离线环境）
#
# 产物：
#   dist/shellcove-<版本>-linux-<架构>.tar.gz        原生安装包（shellcove、install.sh、shellcove.service）
#   dist/shellcove-image-<版本>-linux-<架构>.tar.gz  免编译运行包（预编译二进制 + 运行镜像 + 一键脚本）
#
# 本机没装 go/npm 时，也可以直接用 Docker 取产物：
#   docker build --target artifact --output type=local,dest=./dist .
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
OUT="$ROOT/dist"

# tr 在 GNU/BSD 上行为一致；sed -i 的写法在 macOS 上会因参数差异出错
strip_cr() {
    tr -d '\r' <"$1" >"$1.crlf-tmp"
    mv "$1.crlf-tmp" "$1"
}

VERSION=${1:-}
if [ -z "$VERSION" ]; then
    VERSION=$(sed -n 's/^const AppVersion = "\(.*\)"$/\1/p' "$ROOT/internal/config/config.go" | head -n 1)
fi
if [ -z "$VERSION" ]; then
    echo "无法从 internal/config/config.go 读取版本号，请显式传入：bash deploy/build.sh 0.3.2" >&2
    exit 1
fi
ARCHS=${2:-"amd64 arm64"}

command -v go >/dev/null 2>&1 || {
    echo "缺少 go 命令" >&2
    exit 1
}

if [ "${SKIP_WEB:-0}" != "1" ]; then
    command -v npm >/dev/null 2>&1 || {
        echo "缺少 npm；若前端已构建过，可用 SKIP_WEB=1 bash deploy/build.sh 复用 web/dist" >&2
        exit 1
    }
    echo "==> 构建前端 web/dist"
    (cd "$ROOT/web" && npm ci --no-audit --no-fund && npm run build)
fi
if [ ! -f "$ROOT/web/dist/index.html" ]; then
    echo "web/dist 缺失，go:embed 会编译失败，请先构建前端" >&2
    exit 1
fi

mkdir -p "$OUT"
for arch in $ARCHS; do
    echo "==> 编译 linux/$arch"
    # CGO_ENABLED=0：纯静态二进制，无 glibc 依赖，CentOS 7 到最新发行版通用
    (cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" \
        go build -trimpath -ldflags "-s -w" -o "$OUT/shellcove-linux-$arch" ./cmd/shellcove)

    stage="$OUT/.stage-$arch"
    rm -rf "$stage"
    mkdir -p "$stage"
    cp "$OUT/shellcove-linux-$arch" "$stage/shellcove"
    cp "$ROOT/deploy/install.sh" "$ROOT/deploy/shellcove.service" "$stage/"
    strip_cr "$stage/install.sh"
    strip_cr "$stage/shellcove.service"
    chmod 0755 "$stage/install.sh"

    tar -czf "$OUT/shellcove-$VERSION-linux-$arch.tar.gz" -C "$stage" shellcove install.sh shellcove.service
    rm -rf "$stage"

    # 免编译运行包：内含预编译二进制、容器入口脚本、运行镜像 Dockerfile、compose 与一键部署脚本
    # 目标机器只需拉取 debian:12-slim，不需要安装 Go 与 Node
    img="shellcove-image-$VERSION-linux-$arch"
    rm -rf "$OUT/$img"
    mkdir -p "$OUT/$img"
    cp "$OUT/shellcove-linux-$arch" "$OUT/$img/shellcove"
    cp "$ROOT/docker/entrypoint.sh" "$OUT/$img/entrypoint.sh"
    cp "$ROOT/Dockerfile.runtime" "$OUT/$img/Dockerfile"
    cp "$ROOT/deploy/docker-compose.prebuilt.yml" "$OUT/$img/docker-compose.yml"
    cp "$ROOT/deploy/one-line-deploy.sh" "$OUT/$img/one-line-deploy.sh"
    for f in entrypoint.sh one-line-deploy.sh; do
        strip_cr "$OUT/$img/$f"
        chmod 0755 "$OUT/$img/$f"
    done
    tar -czf "$OUT/$img.tar.gz" -C "$OUT" "$img"
    rm -rf "$OUT/$img" "$OUT/shellcove-linux-$arch"
done

if command -v sha256sum >/dev/null 2>&1; then
    (cd "$OUT" && sha256sum shellcove-*"$VERSION"-linux-*.tar.gz >SHA256SUMS)
else
    (cd "$OUT" && shasum -a 256 shellcove-*"$VERSION"-linux-*.tar.gz >SHA256SUMS)
fi

echo "==> 完成，产物位于 $OUT"
ls -lh "$OUT"/*.tar.gz "$OUT/SHA256SUMS"
