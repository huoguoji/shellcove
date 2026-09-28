#!/usr/bin/env bash
#
# ShellCove 一键安装 / 升级脚本
# 支持：Debian 11+ / Ubuntu 20.04+ / CentOS 7+ / Rocky / AlmaLinux / openEuler / Anolis
#
#   sudo bash install.sh                        # 安装或升级（保留数据库与主密钥）
#   sudo bash install.sh --port 9000 --tz Asia/Shanghai
#   sudo bash install.sh --data-dir /srv/shellcove
#   sudo bash install.sh --uninstall            # 卸载程序，保留数据目录
#
# 本脚本需要与它同目录存在 shellcove 二进制；shellcove.service 仅作参考，脚本会自行生成单元文件。
# 二进制为纯静态编译（CGO_ENABLED=0 + 纯 Go 的 sqlite 驱动），不依赖 glibc 版本，
# 因此同一份产物可直接跑在 CentOS 7 至最新发行版上。
set -euo pipefail

APP_NAME=shellcove
SERVICE_USER=shellcove
PREFIX=/opt/shellcove
DATA_DIR=/var/lib/shellcove
CONF_DIR=/etc/shellcove
ENV_FILE=${CONF_DIR}/shellcove.env
UNIT_FILE=/etc/systemd/system/${APP_NAME}.service
SRC_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

PORT=8080
TZ_NAME=Asia/Shanghai
PORT_SET=0
TZ_SET=0
UNINSTALL=0

log() { printf '\033[32m[shellcove]\033[0m %s\n' "$*"; }
warn() { printf '\033[33m[shellcove]\033[0m %s\n' "$*" >&2; }
die() {
    printf '\033[31m[shellcove]\033[0m %s\n' "$*" >&2
    exit 1
}

usage() {
    cat <<EOF
用法：sudo bash install.sh [选项]

  --port <端口>       监听端口，默认 8080
  --tz <时区>         时区，默认 Asia/Shanghai，如 UTC、Europe/London
  --data-dir <目录>   数据目录，默认 /var/lib/shellcove（含数据库、录像、备份、env.key）
  --uninstall         卸载程序，保留数据目录
  -h, --help          显示帮助
EOF
}

# ---------------------------------------------------------------- 参数解析
while [ "$#" -gt 0 ]; do
    case "$1" in
        --port) [ "$#" -ge 2 ] || die "--port 需要一个端口号"; PORT=$2; PORT_SET=1; shift 2 ;;
        --port=*) PORT=${1#*=}; PORT_SET=1; shift ;;
        --tz) [ "$#" -ge 2 ] || die "--tz 需要一个时区名"; TZ_NAME=$2; TZ_SET=1; shift 2 ;;
        --tz=*) TZ_NAME=${1#*=}; TZ_SET=1; shift ;;
        --data-dir) [ "$#" -ge 2 ] || die "--data-dir 需要一个目录"; DATA_DIR=$2; shift 2 ;;
        --data-dir=*) DATA_DIR=${1#*=}; shift ;;
        --uninstall) UNINSTALL=1; shift ;;
        -h | --help) usage; exit 0 ;;
        *) die "未知参数：$1（用 --help 查看用法）" ;;
    esac
done

case "$PORT" in
    '' | *[!0-9]*) die "--port 必须是 1-65535 之间的数字" ;;
esac
[ "$PORT" -ge 1 ] && [ "$PORT" -le 65535 ] || die "--port 必须是 1-65535 之间的数字"
case "$DATA_DIR" in
    /*) ;;
    *) die "--data-dir 必须是绝对路径" ;;
esac

# ---------------------------------------------------------------- 环境探测
[ "$(id -u)" -eq 0 ] || die "请用 root 运行：sudo bash install.sh"

DISTRO_ID=unknown
DISTRO_NAME=unknown
if [ -r /etc/os-release ]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    DISTRO_ID=${ID:-unknown}
    DISTRO_NAME=${PRETTY_NAME:-$DISTRO_ID}
fi

PKG=none
if command -v apt-get >/dev/null 2>&1; then
    PKG=apt
elif command -v dnf >/dev/null 2>&1; then
    PKG=dnf
elif command -v yum >/dev/null 2>&1; then
    PKG=yum
fi

HAS_SYSTEMD=0
command -v systemctl >/dev/null 2>&1 && HAS_SYSTEMD=1

log "发行版：$DISTRO_NAME（包管理器：$PKG / systemd：$HAS_SYSTEMD）"
case "$DISTRO_ID" in
    debian | ubuntu | raspbian | linuxmint | pop | centos | rhel | rocky | almalinux | fedora | ol | anolis | kylin | uos) ;;
    *) warn "未在已知发行版列表中，仍按通用流程安装（二进制为静态编译，通常可直接运行）" ;;
esac

# ---------------------------------------------------------------- 安装步骤
do_uninstall() {
    if [ "$HAS_SYSTEMD" -eq 1 ]; then
        systemctl stop "$APP_NAME" 2>/dev/null || true
        systemctl disable "$APP_NAME" 2>/dev/null || true
        rm -f "$UNIT_FILE"
        systemctl daemon-reload
    fi
    rm -rf "$PREFIX"
    log "已移除程序与 systemd 单元"
    log "数据目录与主密钥已保留：$DATA_DIR（确认不再需要后自行删除）"
    exit 0
}
if [ "$UNINSTALL" -eq 1 ]; then do_uninstall; fi

install_deps() {
    local pkgs=""
    [ -d /usr/share/zoneinfo ] || pkgs="tzdata"
    if [ -z "$pkgs" ] && [ ! -d /etc/ssl/certs ]; then pkgs="ca-certificates"; fi
    if [ -z "$pkgs" ]; then
        log "系统依赖已齐备，无需安装"
        return
    fi
    log "安装系统依赖：$pkgs"
    case "$PKG" in
        apt)
            DEBIAN_FRONTEND=noninteractive apt-get update -qq \
                && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
                    -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold $pkgs
            ;;
        dnf) dnf install -y -q $pkgs ;;
        yum) yum install -y -q $pkgs ;;
        none)
            warn "未识别包管理器，请自行确保已安装时区数据（tzdata）"
            return
            ;;
    esac || warn "依赖安装失败，可忽略：二进制为静态编译，仅时区/证书可能受影响"
}

ensure_user() {
    if id -u "$SERVICE_USER" >/dev/null 2>&1; then
        return
    fi
    local shell_path=/sbin/nologin
    if command -v nologin >/dev/null 2>&1; then shell_path=$(command -v nologin); fi
    log "创建系统用户：$SERVICE_USER"
    useradd --system --no-create-home --home-dir "$DATA_DIR" --shell "$shell_path" "$SERVICE_USER"
}

ensure_dirs() {
    install -d -m 0755 "$PREFIX"
    install -d -m 0750 "$CONF_DIR"
    install -d -m 0700 -o "$SERVICE_USER" -g "$SERVICE_USER" "$DATA_DIR"
}

generate_key() {
    local key="$DATA_DIR/env.key"
    if [ -s "$key" ]; then
        chown "$SERVICE_USER:$SERVICE_USER" "$key"
        chmod 0600 "$key"
        log "复用已有主密钥：$key"
        return
    fi
    # od 的 -v 不可省略，否则重复字节行会被折叠成 "*"，导致密钥长度不足 32 字节
    (umask 077; head -c 32 /dev/urandom | od -An -v -tx1 | tr -d ' \n' >"$key")
    chown "$SERVICE_USER:$SERVICE_USER" "$key"
    chmod 0600 "$key"
    cat <<EOF

  ============================================================
  已生成主密钥：$key
  请立刻离线备份该文件！它用于加密 SSH 凭证与备份文件，
  一旦丢失，已保存的凭证与历史备份都无法恢复。
  ============================================================

EOF
}

set_env() {
    local k=$1 v=$2
    if grep -q "^${k}=" "$ENV_FILE" 2>/dev/null; then
        sed -i "s|^${k}=.*|${k}=${v}|" "$ENV_FILE"
    else
        printf '%s=%s\n' "$k" "$v" >>"$ENV_FILE"
    fi
}

write_env_file() {
    if [ -f "$ENV_FILE" ]; then
        log "保留已有配置：$ENV_FILE"
        # 只在命令行显式指定时才覆盖，避免误改用户自定的端口/时区
        if [ "$PORT_SET" -eq 1 ]; then set_env PORT "$PORT"; fi
        if [ "$TZ_SET" -eq 1 ]; then set_env TZ "$TZ_NAME"; fi
    else
        log "生成配置：$ENV_FILE"
        cat >"$ENV_FILE" <<EOF
# ShellCove 运行时配置，修改后执行：systemctl restart $APP_NAME
PORT=$PORT
DATA_DIR=$DATA_DIR
ENCRYPTION_KEY_FILE=$DATA_DIR/env.key
# 以 http 直接访问时必须为 false，否则登录 Cookie 带 Secure 标记浏览器不回传
# （表现为「登录成功但立刻又变未登录」）；前置 HTTPS 后可改为 true
SECURE_COOKIE=false
TZ=$TZ_NAME
EOF
    fi
    chmod 0640 "$ENV_FILE"
    chown root:"$SERVICE_USER" "$ENV_FILE"
}

install_binary() {
    local src="$SRC_DIR/$APP_NAME"
    [ -f "$src" ] || die "未找到二进制：$src（请把 install.sh 与 $APP_NAME 放在同一目录）"
    # 先写 .new 再改名为正式文件：直接覆盖运行中的二进制会报 Text file busy
    install -m 0755 -o root -g root "$src" "$PREFIX/$APP_NAME.new"
    mv -f "$PREFIX/$APP_NAME.new" "$PREFIX/$APP_NAME"
    local size
    size=$(du -h "$PREFIX/$APP_NAME" 2>/dev/null | awk '{print $1}') || size=""
    log "已安装二进制：$PREFIX/$APP_NAME${size:+（$size）}"
}

install_unit() {
    log "写入 systemd 单元：$UNIT_FILE"
    cat >"$UNIT_FILE" <<EOF
[Unit]
Description=ShellCove 轻量运维工具
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_USER
WorkingDirectory=$PREFIX
EnvironmentFile=-$ENV_FILE
ExecStart=$PREFIX/$APP_NAME
Restart=on-failure
RestartSec=3
KillSignal=SIGTERM
TimeoutStopSec=30
LimitNOFILE=65535
NoNewPrivileges=true
PrivateTmp=true
SyslogIdentifier=$APP_NAME
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF
    chmod 0644 "$UNIT_FILE"
}

selinux_relabel() {
    if command -v selinuxenabled >/dev/null 2>&1 && selinuxenabled 2>/dev/null; then
        if command -v restorecon >/dev/null 2>&1; then
            restorecon -R "$PREFIX" "$DATA_DIR" >/dev/null 2>&1 || true
            log "已刷新 SELinux 文件上下文（CentOS/RHEL 需要）"
        fi
    fi
}

restart_service() {
    if [ "$HAS_SYSTEMD" -ne 1 ]; then
        warn "未检测到 systemd，请手动启动：DATA_DIR=$DATA_DIR $PREFIX/$APP_NAME"
        return
    fi
    systemctl daemon-reload
    # CentOS 7 自带 systemd 219，不支持 enable --now，这里分开执行
    systemctl enable "$APP_NAME" >/dev/null 2>&1 || true
    systemctl restart "$APP_NAME"
    sleep 2
    if systemctl is-active --quiet "$APP_NAME"; then
        log "服务已启动并设为开机自启"
    else
        warn "服务未处于运行状态，请查看：journalctl -u $APP_NAME -n 50 --no-pager"
    fi
}

print_summary() {
    local ip
    ip=$(hostname -I 2>/dev/null | awk '{print $1}') || true
    [ -n "${ip:-}" ] || ip="<本机IP>"
    cat <<EOF

  ------------------------------------------------------------
  访问地址：http://$ip:$PORT
  初始账号：admin（首次登录强制改密，初始密码见启动日志）
  查询初始密码：journalctl -u $APP_NAME -n 30 --no-pager | grep 初始密码
  数据目录：$DATA_DIR（含 env.key，请定期备份）
  配置文件：$ENV_FILE
  服务日志：journalctl -u $APP_NAME -f

  若主机启用了防火墙，还需放行端口（脚本不会自动改防火墙）：
    Debian / Ubuntu ：ufw allow $PORT/tcp
    CentOS / RHEL   ：firewall-cmd --permanent --add-port=$PORT/tcp && firewall-cmd --reload
  ------------------------------------------------------------

EOF
}

# ---------------------------------------------------------------- 主流程
log "开始安装到 $PREFIX（数据目录 $DATA_DIR）"
install_deps
ensure_user
ensure_dirs
generate_key
write_env_file
install_binary
install_unit
selinux_relabel
restart_service
print_summary
