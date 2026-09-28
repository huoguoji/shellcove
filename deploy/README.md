# ShellCove Linux 部署指南

支持 Debian / Ubuntu / CentOS 全系，提供 **Docker 容器** 与 **原生 systemd** 两条路径，产物均为 amd64 / arm64 双架构。

| 发行版 | Docker 部署 | 原生部署 | 备注 |
| --- | --- | --- | --- |
| Debian 11 / 12 / 13 | ✅ | ✅ | 推荐 docker-ce 官方源，不要用老版 `docker.io` |
| Ubuntu 20.04 / 22.04 / 24.04 | ✅ | ✅ | 同上 |
| CentOS Stream 8 / 9、Rocky、AlmaLinux | ✅ | ✅ | 首选组合 |
| CentOS 7 | ✅ | ✅ | 已 EOL，见 [§4.5](#45-发行版差异与坑) |

原生部署之所以能跨版本通用：二进制用 `CGO_ENABLED=0` + 纯 Go 的 sqlite 驱动（`modernc.org/sqlite`）编译，**完全静态、不依赖 glibc**，CentOS 7（glibc 2.17）到最新发行版都是同一份产物。

---

## 1. 先理解主密钥（两种方式都适用）

`env.key` 用于加密 SSH 凭证、备份文件与登录令牌。**它必须随数据一起备份，丢失后已保存的凭证与历史备份都无法恢复。**

- 优先级：`ENCRYPTION_KEY_FILE` > `ENCRYPTION_KEY`（32 字节，支持 hex / base64 / 32 字符原文）
- 两种部署方式在**首次启动时都会自动生成**密钥文件，不会覆盖已存在的密钥
- 迁移主机时，把整个数据目录（含 `env.key`）一起搬过去即可

---

## 2. 方式一：Docker（三发行版命令一致）

### 2.1 安装 Docker

```bash
curl -fsSL https://get.docker.com | sudo sh
sudo systemctl enable --now docker          # CentOS 7 分开执行：enable 后 start
docker compose version                      # 需要 v2（插件形式）
```

生产环境建议按官方文档配置 docker-ce 软件源，避免发行版自带的旧版本。

### 2.2 启动

```bash
cd shellcove
docker compose up -d --build                # 首次构建镜像并启动
docker compose logs -f | grep 初始密码       # 取初始管理员密码
```

或者用一键脚本（内容就是上面的步骤，另加前置检查、健康等待、密码与访问地址打印）：

```bash
sudo bash deploy/docker-up.sh --port 8080                     # 端口任选，默认 8080
sudo bash deploy/docker-up.sh --port 8080 --admin-password '你的初始密码'
NPM_REGISTRY=https://registry.npmmirror.com GOPROXY=https://goproxy.cn,direct \
  sudo -E bash deploy/docker-up.sh --port 8080                # 国内网络加速构建
```

宿主端口默认 8080，可任选（容器内始终是 8080）：

```bash
echo 'SHELLCOVE_PORT=8022' > .env            # 之后 docker compose 命令自动读取
# 或只对单条命令生效：
SHELLCOVE_PORT=8022 docker compose up -d
```

改完端口如打不开，先确认放行：`sudo ufw allow 8022/tcp`（CentOS：`firewall-cmd --permanent --add-port=8022/tcp && firewall-cmd --reload`）。

不用 compose 的等价命令：

```bash
docker build -t shellcove:0.3.2 .
docker run -d --name shellcove --restart unless-stopped \
  -p 8080:8080 \
  -e TZ=Asia/Shanghai \
  -e SECURE_COOKIE=false \
  -e ADMIN_PASSWORD='改成你的初始密码' \
  -v shellcove-data:/app/data \
  --log-opt max-size=10m --log-opt max-file=3 \
  shellcove:0.3.2
```

（`SHELLCOVE_PORT` 只作用于 compose 的端口映射，`docker run` 直接用 `-p` 指定即可。）

国内网络构建慢时：

```bash
docker compose build --build-arg NPM_REGISTRY=https://registry.npmmirror.com \
                     --build-arg GOPROXY=https://goproxy.cn,direct
```

### 2.3 数据与备份

- 默认命名卷：`shellcove-data` → 宿主路径 `/var/lib/docker/volumes/shellcove_shellcove-data/_data`
- 想直接看到 `env.key` 与数据库，就把它改成宿主目录挂载（**必须先授权，容器内是 uid 10001**）：

```bash
sudo mkdir -p /srv/shellcove/data && sudo chown -R 10001:10001 /srv/shellcove/data
# docker-compose.yml 中替换为：- /srv/shellcove/data:/app/data
```

- 备份：`docker compose stop` 后打包数据目录，或容器内执行备份导出功能；`env.key` 建议单独另存一份
- 录像、上传分片都写在数据目录内，注意卷的磁盘空间

### 2.4 资源限制（CPU / 内存）

compose 里已给容器加上限，可用 `.env` 覆盖。发版口径：空载 RSS 20–40MB，10 个并发 SSH 会话约 128–256MB，唯一的单次峰值是备份导入（请求体上限 256MB）。

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `SHELLCOVE_MEM_LIMIT` | `512m` | 内存硬上限，超出会被 OOM kill。1GB 小机器可降到 `384m`；确定不做大备份导入可降到 `256m` |
| `SHELLCOVE_MEM_RESERVATION` | `128m` | 内存软预留，只影响调度，不限制使用 |
| `SHELLCOVE_CPUS` | `1.0` | CPU 上限。交互式终端的主要开销是 SSH 加解密，20 个会话以内 1 核足够 |
| `SHELLCOVE_PIDS_LIMIT` | `512` | 进程数上限，防异常 fork 撑爆宿主 |

```bash
# 例：2 核 4GB 的机器给到 1.5 核 / 768MB
printf 'SHELLCOVE_CPUS=1.5\nSHELLCOVE_MEM_LIMIT=768m\n' >> .env
docker compose up -d

docker stats --no-stream shellcove        # 看实际占用，再决定要不要调
```

容器日志已限制为 `10m × 3` 个文件，不会慢慢吃满宿主磁盘。

### 2.5 升级

```bash
docker compose up -d --build                # 改完代码重新构建
```

---

## 3. 方式二：原生部署（systemd，Debian / Ubuntu / CentOS 同一套步骤）

### 3.1 取二进制

**有 Docker 的机器**（无需装 Go / Node）：

```bash
docker build --target artifact --output type=local,dest=./dist .
# dist/ 下得到：shellcove、install.sh、shellcove.service
```

**有 Go + Node 的机器**：

```bash
bash deploy/build.sh                        # 产物 dist/shellcove-<版本>-linux-<架构>.tar.gz
SKIP_WEB=1 bash deploy/build.sh             # 复用已有 web/dist，不联网装前端依赖
```

### 3.2 安装

```bash
tar xzf shellcove-0.3.2-linux-amd64.tar.gz
cd shellcove-0.3.2-linux-amd64 2>/dev/null || true   # 直接解压到当前目录的情况
sudo bash install.sh --port 8080 --tz Asia/Shanghai
```

脚本按顺序完成：

1. 探测发行版与包管理器（`apt-get` / `dnf` / `yum`），按需安装 `tzdata`、`ca-certificates`
2. 创建系统用户 `shellcove`（nologin）
3. 创建 `/opt/shellcove`（程序）与 `/var/lib/shellcove`（数据，0700）
4. 生成 `/var/lib/shellcove/env.key`（0600，已存在则复用）
5. 生成 `/etc/shellcove/shellcove.env`（已存在则保留，仅在显式传参时改 PORT / TZ）
6. 安装二进制到 `/opt/shellcove/shellcove`（先写 `.new` 再改名，升级时不会触发 `Text file busy`）
7. 写入 `/etc/systemd/system/shellcove.service`，`enable` + `restart`

其他参数：

```bash
sudo bash install.sh --data-dir /srv/shellcove     # 自定义数据目录
sudo bash install.sh --uninstall                # 卸载程序，保留数据目录
```

### 3.3 日常运维

```bash
systemctl status shellcove
journalctl -u shellcove -f                       # 日志
journalctl -u shellcove -n 30 --no-pager | grep 初始密码
vim /etc/shellcove/shellcove.env && systemctl restart shellcove
```

升级只需把新二进制放到脚本同目录后重跑一次 `sudo bash install.sh`，数据与密钥都不受影响。

### 3.4 放行端口（脚本不会自动改防火墙）

```bash
# Debian / Ubuntu
sudo ufw allow 8080/tcp
# CentOS / RHEL
sudo firewall-cmd --permanent --add-port=8080/tcp && sudo firewall-cmd --reload
```

### 3.5 发行版差异与坑

- **CentOS 7**：已 EOL，`yum` 源需指向 `vault.centos.org` 才能装包（脚本安装失败只告警，不影响静态二进制运行）；systemd 219 不支持 `enable --now`，脚本已分开执行；SELinux 开启时脚本会自动 `restorecon`。
- **CentOS 7 上的 Docker**：docker-ce 25+ 已不提供 RHEL7 包，需用 24.0.x 或改用 Rocky / CentOS Stream 9。
- **Debian 12 slim 类环境**：默认没有 `tzdata`，`TZ` 会被静默忽略（代码里 `time.LoadLocation` 失败不报错），脚本会补装。
- **arm64**：`deploy/build.sh` 直接产出 arm64 包，Docker 用 `buildx --platform linux/arm64`。

---

## 4. 环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `PORT` | `8080` | 监听端口 |
| `DATA_DIR` | Linux 为 `/app/data` | 数据库、录像、上传、备份、密钥的总目录 |
| `ENCRYPTION_KEY_FILE` | 空 | 主密钥文件路径，优先级高于 `ENCRYPTION_KEY` |
| `ENCRYPTION_KEY` | 空 | 主密钥字面量（32 字节 hex），不进配置文件更安全 |
| `SECURE_COOKIE` | **`true`** | **用 http 访问必须设为 `false`**，否则登录后立刻变未登录 |
| `ADMIN_PASSWORD` | 空 | 仅首次初始化数据库时生效，留空则随机生成并打印到日志 |
| `TZ` | `UTC` | 需要系统有 tzdata |
| `IP_WHITELIST` | 空 | 逗号分隔，如 `192.168.1.0/24,10.0.0.5` |
| `SESSION_IDLE_MINUTES` | `30` | 会话空闲保活时长 |
| `RECORDING_MAX_MB` | `64` | 单次会话录像大小上限，0 为不限 |
| `UPLOAD_CHUNK_KB` | `2048` | SFTP 上传分片 |
| `UPLOAD_MAX_FILE_MB` | `4096` | 单文件上传上限 |
| `TRASH_RETENTION_DAYS` / `TRASH_CLEANUP_MINUTES` | `30` / `60` | 回收站保留与清理周期 |

---

## 5. 反向代理与安全

`SECURE_COOKIE=true` 时 Cookie 只在 HTTPS 下发送。要用域名 + HTTPS 暴露，请改回 `true` 并在前面挂 nginx（终端与文件传输是 WebSocket / 长连接，必须带上 Upgrade 头）：

```nginx
server {
    listen 443 ssl;
    server_name shellcove.example.com;
    # ssl_certificate ...; ssl_certificate_key ...;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        # 关键：覆盖客户端伪造的来源 IP
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_read_timeout 3600s;   # 终端长连接，别用默认 60s
        proxy_send_timeout 3600s;
        client_max_body_size 0;     # 文件传输走分片，这里不限
    }
}
```

配套：把 `proxy_pass` 指向实际监听地址（compose 端口由 `SHELLCOVE_PORT` 决定，默认 `127.0.0.1:8080`；原生部署为 `install.sh --port` 的值），并把 `SECURE_COOKIE` 设为 `true`。

> **安全提醒**：程序读取来源 IP 时直接信任 `X-Real-IP` / `X-Forwarded-For`（`internal/api/server.go` 的 `clientIP`），且没有可信代理校验。如果不经过 nginx 直接暴露端口，客户端可以伪造这两个头，从而**绕过 `IP_WHITELIST`**、污染审计日志。因此：要么不直接暴露、统一走上面的 nginx（它会覆盖这两个头），要么用防火墙限制来源网段。

---

## 6. 排障

| 现象 | 原因与处理 |
| --- | --- |
| 启动即失败，提示主密钥读取失败 | 未注入 `ENCRYPTION_KEY_FILE` / `ENCRYPTION_KEY`，且密钥文件不存在或长度不对 |
| 登录成功后立刻回到登录页 | `SECURE_COOKIE` 是 `true` 但用的是 http，改为 `false` |
| 容器报「数据目录不可写」 | bind mount 的宿主目录属主不对：`chown -R 10001:10001 <宿主目录>` |
| 页面能打开但终端连不上 | 反代缺少 `Upgrade` / `Connection` 头，或 `proxy_read_timeout` 太短 |
| 时间显示不对 | 宿主机/容器缺 `tzdata`，或 `TZ` 名称写错（如 `Asia/Shanghai`） |
| CentOS 上服务起不来 | `journalctl -u shellcove -n 50` 看详情；确认 SELinux 已 `restorecon` |
| 大文件上传中断 | 检查数据目录所在磁盘空间与 `UPLOAD_MAX_FILE_MB` |
| 端口被占用 | `ss -lntp \| grep <端口>`；Docker 改 `SHELLCOVE_PORT`，原生改 `PORT` 后重启 |
