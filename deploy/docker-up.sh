#!/usr/bin/env bash
#
# 一键用 Docker 部署 ShellCove（Debian / Ubuntu / CentOS / Rocky / AlmaLinux 通用）
#
#   sudo bash deploy/docker-up.sh                      # 宿主端口 8080
#   sudo bash deploy/docker-up.sh --port 8022          # 宿主端口改成 8022
#   sudo bash deploy/docker-up.sh --port 8022 --admin-password 'MyPassw0rd'
#
# 国内网络加速构建（sudo 必须带 -E，否则变量传不进 docker）：
#   NPM_REGISTRY=https://registry.npmmirror.com GOPROXY=https://goproxy.cn,direct \
#     sudo -E bash deploy/docker-up.sh --port 8022
#
# 幂等：重复执行 = 重新构建并重启；数据都在命名卷 shellcove-data 里，不会丢。
# 卸载：docker compose down（保留数据）；docker compose down -v（连数据一起删，慎用）
set -euo pipefail

PORT=8080
ADMIN_PASSWORD=""

usage() {
    cat <<'EOF'
用法: bash deploy/docker-up.sh [选项]

  --port <端口>              宿主映射端口，默认 8080（容器内始终 8080）
  --admin-password <密码>    预设初始管理员密码；不传则随机生成并打印到日志。
                             仅在数据库首次初始化（users 表为空）时生效。
  -h, --help                 显示本帮助

可用环境变量（也可写进 .env，二者等价）：
  NPM_REGISTRY   npm 源，国内可设 https://registry.npmmirror.com
  GOPROXY        Go 模块代理，国内可设 https://goproxy.cn,direct
  TZ             容器时区，默认 Asia/Shanghai
  SECURE_COOKIE  直接用 http 访问必须为 false（默认已是 false）；前置 HTTPS 后改 true
  IP_WHITELIST   来源 IP 白名单，逗号分隔，如 192.168.1.0/24
EOF
}

while [ $# -gt 0 ]; do
    case "$1" in
        --port)
            PORT=${2:-}
            [ -n "$PORT" ] || { echo "--port 缺少参数" >&2; exit 2; }
            shift 2
            ;;
        --port=*) PORT=${1#*=}; shift ;;
        --admin-password)
            ADMIN_PASSWORD=${2:-}
            [ -n "$ADMIN_PASSWORD" ] || { echo "--admin-password 缺少参数" >&2; exit 2; }
            shift 2
            ;;
        --admin-password=*) ADMIN_PASSWORD=${1#*=}; shift ;;
        -h|--help) usage; exit 0 ;;
        *) echo "未知参数：$1" >&2; usage >&2; exit 2 ;;
    esac
done

case "$PORT" in
    ''|*[!0-9]*) echo "端口必须是数字：$PORT" >&2; exit 2 ;;
esac
if [ "$PORT" -lt 1 ] || [ "$PORT" -gt 65535 ]; then
    echo "端口超出范围：$PORT" >&2
    exit 2
fi

# ---------------------------------------------------------------- 定位项目根目录
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
if [ -f "$HERE/docker-compose.yml" ]; then
    ROOT=$HERE
elif [ -f "$HERE/../docker-compose.yml" ]; then
    ROOT=$(cd "$HERE/.." && pwd)
else
    echo "找不到 docker-compose.yml：请把脚本放在项目根目录或根目录下的 deploy/ 中执行" >&2
    exit 1
fi
cd "$ROOT"
echo "==> 项目目录：$ROOT"

# ---------------------------------------------------------------- 前置检查
command -v docker >/dev/null 2>&1 || {
    echo "未找到 docker。Debian/Ubuntu 安装：curl -fsSL https://get.docker.com | sudo sh" >&2
    exit 1
}
docker info >/dev/null 2>&1 || {
    echo "无法连接 Docker 守护进程：请用 sudo 重跑，或把当前用户加入 docker 组后重新登录" >&2
    exit 1
}

if docker compose version >/dev/null 2>&1; then
    compose() { docker compose "$@"; }
    echo "==> $(docker compose version | head -n1)"
elif command -v docker-compose >/dev/null 2>&1; then
    compose() { docker-compose "$@"; }
    echo "==> $(docker-compose version | head -n1)"
else
    echo "缺少 compose：请安装 docker-compose-plugin（或 docker-compose）" >&2
    exit 1
fi

echo "==> Docker: $(docker version --format '{{.Server.Version}}' 2>/dev/null || echo 未知)  架构: $(uname -m)  内核: $(uname -r)"

if command -v ss >/dev/null 2>&1; then
    if ss -lnt 2>/dev/null | awk '{print $4}' | grep -qE "[:.]${PORT}$"; then
        echo "!! 宿主 ${PORT} 端口已被占用，启动时可能失败：" >&2
        ss -lntp 2>/dev/null | grep -E "[:.]${PORT}\b" >&2 || true
    fi
fi

if command -v df >/dev/null 2>&1; then
    AVAIL=$(df -Pm /var/lib/docker 2>/dev/null | awk 'NR==2{print $4}')
    if [ -n "${AVAIL:-}" ] && [ "$AVAIL" -lt 4096 ] 2>/dev/null; then
        echo "!! /var/lib/docker 可用空间仅 ${AVAIL}MB，建议至少预留 4GB" >&2
    fi
fi

# ---------------------------------------------------------------- 生成 .env
if [ -f .env ]; then
    cp .env ".env.bak.$(date +%Y%m%d%H%M%S)"
    echo "==> 已备份原 .env"
fi
touch .env

# 用 grep -v 重写而非 sed，避免密码里的 & | / 被当成 sed 语法
set_env() {
    KEY=$1
    VAL=$2
    if grep -q "^${KEY}=" .env; then
        grep -v "^${KEY}=" .env >.env.tmp || true
        mv .env.tmp .env
    fi
    printf '%s=%s\n' "$KEY" "$VAL" >>.env
}

set_env SHELLCOVE_PORT "$PORT"
if [ -n "$ADMIN_PASSWORD" ]; then
    set_env ADMIN_PASSWORD "$ADMIN_PASSWORD"
    echo "==> 已写入 ADMIN_PASSWORD（仅首次初始化数据库时生效）"
fi
echo "==> 宿主端口：${PORT}（容器内 8080）"

# ---------------------------------------------------------------- 构建并启动
echo
echo "==> [1/3] 构建镜像（首次需拉取 node/go/debian 基础镜像，视网络 3-15 分钟）"
if docker compose version >/dev/null 2>&1; then
    compose build --progress plain
else
    compose build
fi

echo
echo "==> [2/3] 启动容器"
compose up -d

echo
echo "==> [3/3] 等待健康检查（最多 120 秒）"
CID=$(compose ps -q shellcove 2>/dev/null | head -n1 || true)
if [ -z "$CID" ]; then
    echo "容器未启动，下面是最近日志：" >&2
    compose logs --no-color --tail=80 || true
    exit 1
fi

STATUS=unknown
i=0
while [ "$i" -lt 60 ]; do
    STATUS=$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$CID" 2>/dev/null || echo unknown)
    case "$STATUS" in
        healthy) break ;;
        unhealthy)
            echo "健康检查失败，下面是最近日志：" >&2
            compose logs --no-color --tail=80 || true
            exit 1
            ;;
    esac
    sleep 2
    i=$((i + 1))
done

# ---------------------------------------------------------------- 结果汇总
echo
compose ps
echo
if [ "$STATUS" = "healthy" ]; then
    echo "==> 状态：healthy"
else
    echo "==> 状态：${STATUS}（未在 120 秒内 healthy，可继续观察：docker compose logs -f shellcove）"
fi

IP=$(hostname -I 2>/dev/null | awk '{print $1}')
[ -n "${IP:-}" ] || IP="<服务器IP>"
echo
echo "==> 访问地址：http://${IP}:${PORT}    （服务器本机：http://127.0.0.1:${PORT}）"
echo "==> 用 http 直连时请保持 SECURE_COOKIE=false，否则会「登录成功但立刻退出」"

echo
echo "==> 初始管理员密码（仅在首次初始化数据库时打印一次）："
if compose logs --no-color shellcove 2>/dev/null | grep -q '初始密码'; then
    compose logs --no-color shellcove | grep '初始密码' | tail -n 1
else
    echo "（本次没有输出初始密码：说明数据库里已有管理员，或你用 --admin-password 指定了密码）"
fi

echo
echo "==> 常用命令"
echo "    查看日志：docker compose logs -f shellcove"
echo "    重启：    docker compose restart"
echo "    停止：    docker compose down        # 保留数据卷 shellcove-data"
echo "    彻底删除：docker compose down -v     # 连数据与 env.key 一起删，务必先备份"
echo "    数据位置：/var/lib/docker/volumes/shellcove_shellcove-data/_data"
