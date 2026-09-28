#!/usr/bin/env bash
#
# ShellCove 一条命令部署（Docker / 宝塔面板通用，零手动解压）
#
# 用法一（推荐，服务器终端粘贴一条命令）：
#   curl -fsSL <本脚本地址> | sudo bash -s -- --src <运行包地址> --port 8080
#
# 用法二（把本脚本与运行包放在同一可访问目录）：
#   curl -fsSL <目录>/one-line-deploy.sh | sudo bash -s -- --src <目录>/shellcove-image-0.3.2-linux-amd64.tar.gz
#
# 用法三（包已在服务器本地，无需下载）：
#   sudo bash one-line-deploy.sh --src /tmp/shellcove-image-0.3.2-linux-amd64.tar.gz
#
# 可选环境变量（也可写成 --xxx 参数）：
#   SHELLCOVE_SRC        运行包地址（.tar.gz）
#   SHELLCOVE_PORT       宿主端口，默认 8080
#   SHELLCOVE_VERSION    镜像标签，默认 0.3.2
#   SHELLCOVE_BIND       监听地址，默认 0.0.0.0（前置 Nginx 可设 127.0.0.1）
#   ADMIN_PASSWORD    初始管理员密码，留空则随机生成并打印在容器日志
#   APT_MIRROR        构建期 apt 源；留空 = Debian 官方源（海外服务器最快）
#   SECURE_COOKIE     默认 false；前置 HTTPS 后设 true
#
# 幂等：重复执行 = 重新构建镜像 + 重建容器；数据在命名卷 shellcove-data 里，不会丢。
set -euo pipefail

SRC=${SHELLCOVE_SRC:-}
PORT=${SHELLCOVE_PORT:-8080}
VERSION=${SHELLCOVE_VERSION:-0.3.2}
BIND=${SHELLCOVE_BIND:-0.0.0.0}
ADMIN_PASSWORD=${ADMIN_PASSWORD:-}
# 海外服务器用官方源最快；国内服务器建议传 --apt-mirror mirrors.aliyun.com
APT_MIRROR=${APT_MIRROR:-}
SECURE_COOKIE=${SECURE_COOKIE:-false}
WORKDIR=${SHELLCOVE_DIR:-/www/wwwroot/shellcove}
KEEP_TAR=0

log() { printf '\033[32m[shellcove]\033[0m %s\n' "$*"; }
warn() { printf '\033[33m[shellcove]\033[0m %s\n' "$*" >&2; }
die() {
    printf '\033[31m[shellcove]\033[0m %s\n' "$*" >&2
    exit 1
}

usage() {
    cat <<EOF
用法：sudo bash one-line-deploy.sh --src <运行包地址> [选项]

  --src <url|路径>     运行包（shellcove-image-<版本>-linux-<架构>.tar.gz），必填
  --port <端口>        宿主映射端口，默认 8080（容器内始终 8080）
  --bind <地址>        监听地址，默认 0.0.0.0；前置 Nginx 可设 127.0.0.1
  --dir <目录>         解压与构建目录，默认 /www/wwwroot/shellcove
  --version <版本>     镜像标签，默认 0.3.2
  --admin-password <密码>  初始管理员密码（仅首次初始化数据库时生效）
  --secure-cookie <true|false>  Cookie 是否带 Secure，默认 false（http 直连必须 false，
                                前置 HTTPS 后改 true，否则登录状态无法保持）
  --apt-mirror <域名>  构建期 apt 源；默认用 Debian 官方源（海外服务器最快），
                       国内服务器建议写 --apt-mirror mirrors.aliyun.com
  --ip-whitelist <列表>  来源 IP 白名单，逗号分隔，如 203.0.113.0/24,198.51.100.7
                         公网服务器强烈建议设置（应用层拒绝，比防火墙更靠前）
  -h, --help           显示本帮助
EOF
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --src) SRC=${2:-}; [ -n "$SRC" ] || die "--src 缺少参数"; shift 2 ;;
        --src=*) SRC=${1#*=}; shift ;;
        --port) PORT=${2:-}; shift 2 ;;
        --port=*) PORT=${1#*=}; shift ;;
        --bind) BIND=${2:-}; shift 2 ;;
        --bind=*) BIND=${1#*=}; shift ;;
        --dir) WORKDIR=${2:-}; shift 2 ;;
        --dir=*) WORKDIR=${1#*=}; shift ;;
        --version) VERSION=${2:-}; shift 2 ;;
        --version=*) VERSION=${1#*=}; shift ;;
        --admin-password) ADMIN_PASSWORD=${2:-}; shift 2 ;;
        --admin-password=*) ADMIN_PASSWORD=${1#*=}; shift ;;
        --apt-mirror) APT_MIRROR=${2:-}; shift 2 ;;
        --apt-mirror=*) APT_MIRROR=${1#*=}; shift ;;
        --secure-cookie) SECURE_COOKIE=${2:-}; shift 2 ;;
        --secure-cookie=*) SECURE_COOKIE=${1#*=}; shift ;;
        --ip-whitelist) IP_WHITELIST=${2:-}; shift 2 ;;
        --ip-whitelist=*) IP_WHITELIST=${1#*=}; shift ;;
        -h | --help) usage; exit 0 ;;
        *) die "未知参数：$1（用 --help 查看用法）" ;;
    esac
done

[ -n "$SRC" ] || {
    usage >&2
    die "必须提供 --src（运行包地址）"
}
case "$PORT" in
    '' | *[!0-9]*) die "--port 必须是数字" ;;
esac
[ "$PORT" -ge 1 ] && [ "$PORT" -le 65535 ] || die "--port 必须在 1-65535 之间"

# ---------------------------------------------------------------- 环境检查
[ "$(id -u)" -eq 0 ] || die "请用 root 运行：curl -fsSL <地址> | sudo bash -s -- --src <包地址>"
command -v docker >/dev/null 2>&1 || die "未安装 Docker。Debian/Ubuntu：curl -fsSL https://get.docker.com | sh；CentOS：yum install -y docker-ce（或用宝塔面板 Docker 模块安装）"
docker info >/dev/null 2>&1 || die "无法连接 Docker 守护进程：确认 docker 服务已启动（systemctl start docker）"

# 架构必须与运行包匹配，这里只做提示（包名里带架构，用户自己选）
ARCH=$(uname -m)
case "$ARCH" in
    x86_64 | amd64) SUGGEST=amd64 ;;
    aarch64 | arm64) SUGGEST=arm64 ;;
    *) SUGGEST=unknown ;;
esac
log "宿主：$ARCH（对应运行包架构：$SUGGEST） / Docker：$(docker version --format '{{.Server.Version}}' 2>/dev/null || echo 未知）"
case "$SRC" in
    *arm64*)
        [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ] || warn "包名含 arm64，但宿主是 $ARCH，架构不匹配会启动失败" ;;
    *amd64*)
        [ "$ARCH" = "x86_64" ] || [ "$ARCH" = "amd64" ] || warn "包名含 amd64，但宿主是 $ARCH，架构不匹配会启动失败" ;;
esac

# ---------------------------------------------------------------- 取运行包
mkdir -p "$WORKDIR"
cd "$WORKDIR"

TARBALL=""
STAGE=""
cleanup() {
    if [ "$KEEP_TAR" -ne 1 ] && [ -n "$TARBALL" ] && [ -f "$TARBALL" ]; then
        rm -f "$TARBALL"
    fi
    if [ -n "$STAGE" ] && [ -d "$STAGE" ]; then
        rm -rf "$STAGE"
    fi
    return 0
}
trap cleanup EXIT

case "$SRC" in
    http://* | https://*)
        TARBALL="$WORKDIR/pkg-download.tar.gz"
        log "下载运行包：$SRC"
        if command -v curl >/dev/null 2>&1; then
            curl -fSL --retry 3 --connect-timeout 20 -o "$TARBALL" "$SRC"
        elif command -v wget >/dev/null 2>&1; then
            wget -O "$TARBALL" "$SRC"
        else
            die "缺少 curl / wget，无法下载；可先手动下载到 $WORKDIR 再用 --src <本地路径>"
        fi
        ;;
    *)
        TARBALL=$(cd "$(dirname "$SRC")" && pwd)/$(basename "$SRC")
        [ -f "$TARBALL" ] || die "找不到运行包：$SRC"
        ;;
esac
log "运行包大小：$(du -h "$TARBALL" | awk '{print $1}')"

STAGE=$(mktemp -d "$WORKDIR/.stage.XXXXXX")
tar -xzf "$TARBALL" -C "$STAGE"
# 包内可能有一层顶层目录（shellcove-image-0.3.2-linux-amd64/），统一拍平
PKGROOT="$STAGE"
if [ ! -f "$STAGE/Dockerfile" ]; then
    SUB=$(find "$STAGE" -maxdepth 2 -name Dockerfile -type f -printf '%h\n' 2>/dev/null | head -n 1 || true)
    [ -n "$SUB" ] || die "运行包里找不到 Dockerfile，请确认下载的是 shellcove-image-*.tar.gz"
    PKGROOT="$SUB"
fi
[ -f "$PKGROOT/shellcove" ] || die "运行包里找不到 shellcove 二进制"
log "运行包内容：$(ls "$PKGROOT" | tr '\n' ' ')"

# ---------------------------------------------------------------- 构建镜像
if [ -n "$APT_MIRROR" ]; then
    log "构建镜像 shellcove:$VERSION（apt 源：$APT_MIRROR）"
else
    log "构建镜像 shellcove:$VERSION（apt 源：Debian 官方源，只装 3 个包，约 1 分钟）"
fi
# APT_MIRROR 必须显式传入（即使是空值）：Dockerfile 里该参数默认是国内源，
# 海外服务器用国内源反而慢，传空值才会保持镜像自带的官方源。
docker build --build-arg "APT_MIRROR=$APT_MIRROR" -t "shellcove:$VERSION" "$PKGROOT"
docker image inspect "shellcove:$VERSION" >/dev/null 2>&1 || die "镜像构建失败"
log "镜像就绪：shellcove:$VERSION"

# ---------------------------------------------------------------- 启动容器
# 数据放命名卷，重复部署不丢 env.key 与数据库
if docker container inspect shellcove >/dev/null 2>&1; then
    log "已存在同名容器，先移除（数据卷 shellcove-data 保留）"
    docker rm -f shellcove >/dev/null
fi

# 端口占用检查（放在移除旧容器之后，否则会把刚释放的旧容器端口算成占用）
PORT_BUSY=0
if command -v ss >/dev/null 2>&1; then
    PORT_BUSY=$(ss -lnt 2>/dev/null | awk '{print $4}' | grep -c "[:.]$PORT\$" || true)
elif command -v netstat >/dev/null 2>&1; then
    PORT_BUSY=$(netstat -lnt 2>/dev/null | awk '{print $4}' | grep -c "[:.]$PORT\$" || true)
fi
if [ "${PORT_BUSY:-0}" -gt 0 ]; then
    warn "宿主端口 $PORT 已被占用，占用者："
    ss -lntp 2>/dev/null | grep "[:.]$PORT " >&2 || netstat -lntp 2>/dev/null | grep "[:.]$PORT " >&2 || true
    die "请换端口重试，例如 --port 8090"
fi

# host-gateway 便于容器内用 host.docker.internal 访问宿主机（SSH 到本机时会用到）
RUN_ARGS=(
    -d
    --name shellcove
    --restart unless-stopped
    --stop-timeout 30
    --security-opt no-new-privileges:true
    --pids-limit 512
    --add-host host.docker.internal:host-gateway
    --memory 512m
    --memory-reservation 128m
    --cpus 1.0
    --log-opt max-size=10m
    --log-opt max-file=3
    -p "$BIND:$PORT:8080"
    -v shellcove-data:/app/data
    -e DATA_DIR=/app/data
    -e PORT=8080
    -e "TZ=${TZ:-Asia/Shanghai}"
    -e "SECURE_COOKIE=$SECURE_COOKIE"
)
if [ -n "$ADMIN_PASSWORD" ]; then
    RUN_ARGS+=(-e "ADMIN_PASSWORD=$ADMIN_PASSWORD")
fi
if [ -n "${IP_WHITELIST:-}" ]; then
    RUN_ARGS+=(-e "IP_WHITELIST=$IP_WHITELIST")
fi

log "启动容器：宿主 ${BIND}:${PORT} -> 容器 8080"
docker run "${RUN_ARGS[@]}" "shellcove:$VERSION" >/dev/null

# ---------------------------------------------------------------- 健康检查
log "等待健康检查（最多 90 秒）"
STATUS=unknown
i=0
while [ "$i" -lt 45 ]; do
    STATUS=$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' shellcove 2>/dev/null || echo unknown)
    if [ "$STATUS" = "healthy" ] || [ "$STATUS" = "unhealthy" ]; then
        break
    fi
    sleep 2
    i=$((i + 1))
done

IP=$(hostname -I 2>/dev/null | awk '{print $1}')
[ -n "${IP:-}" ] || IP="<服务器IP>"

if [ "$STATUS" != "healthy" ]; then
    warn "健康检查未通过（$STATUS），以下是最近日志："
    docker logs --tail 60 shellcove 2>&1 || true
    exit 1
fi
log "容器状态：healthy"

# ---------------------------------------------------------------- 结果
echo
echo "  ------------------------------------------------------------"
echo "  访问地址：http://${IP}:${PORT}"
echo "  初始账号：admin（首次登录强制改密）"
if docker logs shellcove 2>&1 | grep -q '初始密码'; then
    echo "  初始密码：$(docker logs shellcove 2>&1 | grep '初始密码' | tail -n 1)"
else
    echo "  初始密码：（无输出：数据库已初始化过，或你用了 --admin-password）"
fi
echo "  数据卷：  shellcove-data -> $(docker volume inspect -f '{{.Mountpoint}}' shellcove-data 2>/dev/null || echo '?')"
echo "  查看日志：docker logs -f shellcove"
echo "  停止：    docker rm -f shellcove        # 数据卷保留"
echo
echo "  还需放行端口（脚本不会改宿主防火墙）："
echo "    宝塔面板 → 安全 → 放行 ${PORT}/tcp"
echo "    ufw：ufw allow ${PORT}/tcp"
echo "    firewalld：firewall-cmd --permanent --add-port=${PORT}/tcp && firewall-cmd --reload"
echo "    云服务器：控制台安全组同样要放行 ${PORT}/tcp（海外机器通常就是这一层）"
echo
echo "  公网服务器安全建议："
echo "    - 安全组里只放行你自己的出口 IP，比 0.0.0.0/0 安全得多"
echo "    - 或加应用层白名单：重跑时加 --ip-whitelist <你的IP或网段>"
echo "    - 长期建议：Nginx 反代 + HTTPS，然后 --bind 127.0.0.1 --secure-cookie true"
echo
echo "  重要：env.key 与数据库是一对，丢了 env.key 已加密的 SSH 凭证无法恢复，请定期备份数据卷。"
echo "  ------------------------------------------------------------"
