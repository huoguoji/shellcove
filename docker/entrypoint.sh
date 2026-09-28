#!/bin/sh
#
# 容器入口脚本：首次启动时自动生成主密钥，
# 让 docker run 开箱即用；已有主密钥时严格复用，绝不覆盖。
#
# 密钥优先级（与 internal/crypto.LoadMasterKey 一致）：
#   ENCRYPTION_KEY_FILE > ENCRYPTION_KEY
set -eu

DATA_DIR="${DATA_DIR:-/app/data}"
KEY_FILE="${ENCRYPTION_KEY_FILE:-$DATA_DIR/env.key}"

# 数据目录可写性检查：bind mount 时最常见的问题就是宿主目录属主不是 10001
if ! mkdir -p "$DATA_DIR" 2>/dev/null || ! touch "$DATA_DIR/.write-test" 2>/dev/null; then
    echo "[entrypoint] 数据目录不可写：$DATA_DIR" >&2
    echo "[entrypoint] bind mount 场景请先授权：chown -R 10001:10001 <宿主目录>" >&2
    exit 1
fi
rm -f "$DATA_DIR/.write-test"

if [ -z "${ENCRYPTION_KEY:-}" ] && [ ! -s "$KEY_FILE" ]; then
    umask 077
    # od 的 -v 不可省略，否则重复字节行会被折叠成 "*"，导致密钥长度不足 32 字节
    head -c 32 /dev/urandom | od -An -v -tx1 | tr -d ' \n' > "$KEY_FILE"
    chmod 600 "$KEY_FILE"
    echo "[entrypoint] 已生成主密钥：$KEY_FILE" >&2
    echo "[entrypoint] 请立即离线备份该文件；一旦丢失，已加密的 SSH 凭证与历史备份都无法恢复。" >&2
fi

if [ -z "${ENCRYPTION_KEY:-}" ] && [ -s "$KEY_FILE" ]; then
    ENCRYPTION_KEY_FILE="$KEY_FILE"
    export ENCRYPTION_KEY_FILE
fi

exec "$@"
