# ShellCove 镜像：容器内完成「前端构建 → 后端静态编译」，运行镜像基于 debian:12-slim，
# 因此与宿主发行版（Debian / Ubuntu / CentOS / Rocky / AlmaLinux）完全无关。
#
#   docker build -t shellcove:0.3.2 .
#   docker compose up -d
#   docker buildx build --platform linux/amd64,linux/arm64 -t shellcove:0.3.2 --push .
#   docker build --target artifact --output type=local,dest=./dist .    # 只导出原生部署离线包
#
# 软件源默认走国内镜像：npm 用阿里 npmmirror、Go module 用七牛 goproxy.cn、apt 用阿里云。
# 海外机器想回退官方源，把三个都清掉即可：
#   --build-arg NPM_REGISTRY=https://registry.npmjs.org \
#   --build-arg GOPROXY=https://proxy.golang.org,direct \
#   --build-arg APT_MIRROR=

ARG NODE_IMAGE=node:20-alpine
ARG GO_IMAGE=golang:1.22-bookworm
ARG RUNTIME_IMAGE=debian:12-slim

# ------------------------------------------------------------------ 前端构建
FROM ${NODE_IMAGE} AS web-builder
# npm 源可选线路（改默认值或用 --build-arg NPM_REGISTRY=... 覆盖）：
#   阿里 npmmirror   https://registry.npmmirror.com                    （默认，同步最勤）
#   腾讯云           https://mirrors.cloud.tencent.com/npm/
#   华为云           https://mirrors.huaweicloud.com/repository/npm/
#   官方             https://registry.npmjs.org
ARG NPM_REGISTRY=https://registry.npmmirror.com
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --registry="${NPM_REGISTRY}" --no-audit --no-fund
COPY web/index.html web/vite.config.js ./
COPY web/src ./src
RUN npm run build

# ------------------------------------------------------------------ 后端编译
# CGO_ENABLED=0 + modernc.org/sqlite（纯 Go 实现）→ 完全静态，不依赖 glibc，
# 同一份产物可在 CentOS 7（glibc 2.17）到最新发行版上直接运行。
FROM ${GO_IMAGE} AS go-builder
# Go module 源可选线路（改默认值或用 --build-arg GOPROXY=... 覆盖）：
#   七牛 goproxy.cn  https://goproxy.cn,direct                         （默认，国内最稳）
#   阿里云           https://mirrors.aliyun.com/goproxy/,direct
#   中科大           https://mirrors.ustc.edu.cn/goproxy/,direct
#   腾讯云           https://mirrors.cloud.tencent.com/go/,direct
#   华为云           https://mirrors.huaweicloud.com/repository/goproxy/,direct
#   官方             https://proxy.golang.org,direct
ARG GOPROXY=https://goproxy.cn,direct
ENV CGO_ENABLED=0 GOOS=linux
WORKDIR /src
COPY go.mod go.sum ./
RUN go env -w GOPROXY="${GOPROXY}" && go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY web/embed.go ./web/embed.go
# 前端产物走 go:embed 打进二进制，实现单文件部署
COPY --from=web-builder /src/web/dist ./web/dist
RUN go build -trimpath -ldflags "-s -w" -o /out/shellcove ./cmd/shellcove

# ------------------------------------------------------------------ 运行镜像
FROM ${RUNTIME_IMAGE} AS runtime
# apt 源可选线路（改默认值或用 --build-arg APT_MIRROR=... 覆盖；传空值 = 镜像自带官方源）：
#   阿里云    mirrors.aliyun.com            （默认，主源 + security 均完整）
#   清华大学  mirrors.tuna.tsinghua.edu.cn
#   中科大    mirrors.ustc.edu.cn
#   腾讯云    mirrors.cloud.tencent.com
#   华为云    mirrors.huaweicloud.com
#   网易      mirrors.163.com               （无 debian-security 镜像，security 源会 404）
#   官方源    deb.debian.org                （海外机器直连官方更快）
ARG APT_MIRROR=mirrors.aliyun.com
ENV DEBIAN_FRONTEND=noninteractive
# tzdata 用于 TZ 环境变量（缺失时 time.LoadLocation 会静默退回 UTC），curl 供 HEALTHCHECK 使用
# Debian 12 用 /etc/apt/sources.list.d/debian.sources（deb822），11 用 /etc/apt/sources.list，两个都处理
RUN set -eux; \
    if [ -n "${APT_MIRROR}" ]; then \
        for f in /etc/apt/sources.list /etc/apt/sources.list.d/debian.sources; do \
            [ -f "$f" ] || continue; \
            sed -i "s|deb.debian.org|${APT_MIRROR}|g; s|security.debian.org|${APT_MIRROR}|g" "$f"; \
        done; \
    fi; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates curl tzdata; \
    rm -rf /var/lib/apt/lists/*

# DATA_DIR 默认值本来就是 /app/data（见 internal/config/config.go）；
# 以 http 直接暴露时必须关闭 Secure Cookie，否则浏览器不回传登录 Cookie。
ENV DATA_DIR=/app/data \
    PORT=8080 \
    TZ=Asia/Shanghai \
    SECURE_COOKIE=false

# 固定 uid:gid = 10001，bind mount 宿主目录时按此授权：chown -R 10001:10001 <宿主目录>
RUN groupadd -g 10001 shellcove \
    && useradd -u 10001 -g 10001 -M -d /app -s /usr/sbin/nologin shellcove \
    && mkdir -p /app/data \
    && chown -R shellcove:shellcove /app

COPY --from=go-builder /out/shellcove /app/shellcove
COPY docker/entrypoint.sh /usr/local/bin/shellcove-entrypoint
# sed 兜底：脚本若被 CRLF 换行编辑过，shebang 会失效（跨平台编辑器 / git autocrlf 都可能引入）
RUN sed -i 's/\r$//' /usr/local/bin/shellcove-entrypoint \
    && chmod 0755 /usr/local/bin/shellcove-entrypoint

WORKDIR /app
USER 10001:10001
VOLUME ["/app/data"]
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD curl -fsS "http://127.0.0.1:${PORT}/api/health" >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/shellcove-entrypoint"]
CMD ["/app/shellcove"]

# ------------------------------------------------------------------ 离线包导出
# 无 Docker 的 Debian / Ubuntu / CentOS 主机走原生安装，用这条命令取出所需文件：
#   docker build --target artifact --output type=local,dest=./dist .
FROM scratch AS artifact
COPY --from=go-builder /out/shellcove /shellcove
COPY deploy/install.sh /install.sh
COPY deploy/shellcove.service /shellcove.service
