# ShellCove

自托管的 Web 终端管理平台：在浏览器里集中管理多台 Linux 主机，提供 SSH 终端、SFTP 文件管理、多用户权限隔离、会话录像与操作审计。

后端为 Go 单二进制（前端产物经 `go:embed` 内嵌），数据只用 SQLite 单文件，**无外部依赖、无需 CGO**，兼容 CentOS 7 到最新发行版。

## 功能

### 终端与文件

- 浏览器内多会话 SSH 终端（xterm.js 标签页）：窗口自适应、终端内搜索、链接可点击
- SFTP 文件管理：浏览、新建目录、重命名、改权限、删除、下载、文本预览（≤1MB）
- 大文件分片上传，支持**断点续传**；小文件直接上传
- 会话内文件传输面板，本机与远端互传
- 命令片段库：按分组收藏常用命令，一键发送到当前会话

### 主机与凭证

- 主机按文件夹树分组管理，支持跳板机 / 代理链
- 登录凭证 AES-256-GCM 加密入库；查看明文需二次验证，短时间内频繁查看会额外记告警审计

### 多用户与权限

- 用户管理：角色（管理员 / 普通用户）、启用禁用、密码重置
- 资源级授权：按文件夹或单台主机授予访问权，SFTP、监控权限可单独放开
- 活动会话总览，管理员可强制断开指定会话

### 安全

- TOTP 两步验证，可强制全员启用；附带 10 个一次性恢复码
- 初始管理员密码可指定；留空则随机生成并打印到日志，且**强制首次登录修改**
- JWT + HttpOnly Cookie，登出即吊销（令牌黑名单）
- IP 白名单，支持 CIDR 与通配符
- 主密钥独立于数据库（`env.key`），凭证与备份文件级加密

### 审计与运维

- 操作审计日志：登录、连接、文件操作、权限变更等，支持按动作类型筛选与清理
- 会话录像（asciinema v2 格式）与命令记录，可在网页回放
- 主机监控快照：CPU / 内存 / 磁盘 / 网络
- 备份导出与导入：整库加密导出，导入时若主密钥不同会自动重加密
- 回收站：删除的主机可恢复或彻底清除，按保留天数自动清理
- 健康检查接口 `GET /api/health`

## 快速开始

### Docker Compose（推荐）

```bash
git clone https://github.com/huoguoji/shellcove.git
cd shellcove
docker compose up -d --build
docker compose logs -f | grep 初始密码
```

浏览器打开 `http://<服务器IP>:8080`，用 `admin` 和日志里的初始密码登录，首次登录会要求改密。

国内网络构建慢时用镜像加速：

```bash
docker compose build --build-arg NPM_REGISTRY=https://registry.npmmirror.com \
                     --build-arg GOPROXY=https://goproxy.cn,direct
docker compose up -d
```

宿主端口默认 8080，可覆盖：

```bash
echo 'SHELLCOVE_PORT=8022' > .env && docker compose up -d
```

### 一键脚本

```bash
sudo bash deploy/docker-up.sh --port 8080
```

脚本包含前置检查、构建、启动、健康等待，并打印初始密码与访问地址。

### 免编译部署（离线包）

用 `Dockerfile.runtime` 配合预编译的静态二进制，服务器只需拉取 `debian:12-slim`（约 30MB），不必装 Go / Node：

```bash
tar xzf shellcove-image-0.3.2-linux-amd64.tar.gz
cd shellcove-image-0.3.2-linux-amd64
sudo bash one-line-deploy.sh --port 8080
```

### 原生部署（systemd，无 Docker）

```bash
tar xzf shellcove-0.3.2-linux-amd64.tar.gz
sudo bash install.sh --port 8080 --tz Asia/Shanghai
```

支持 Debian / Ubuntu / CentOS / Rocky / AlmaLinux，脚本会创建系统用户、数据目录与 `env.key`，并注册 systemd 服务。

> 完整的部署说明——发行版差异与坑、反向代理配置、资源限制、升级与排障——见 **[deploy/README.md](deploy/README.md)**。

## 配置

全部通过环境变量配置，常用项：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `PORT` | `8080` | 监听端口 |
| `DATA_DIR` | `/app/data` | 数据目录：数据库、录像、上传分片、备份、密钥 |
| `SECURE_COOKIE` | `true` | **用 http 直接访问必须设为 `false`**，否则登录后立刻失效 |
| `ADMIN_PASSWORD` | 空 | 仅首次初始化数据库时生效，留空则随机生成并打印到日志 |
| `TZ` | `UTC` | 如 `Asia/Shanghai`（宿主机需有 tzdata） |
| `ENCRYPTION_KEY_FILE` | 空 | 主密钥文件路径，优先级高于 `ENCRYPTION_KEY` |
| `ENCRYPTION_KEY` | 空 | 主密钥字面量（32 字节），建议用文件方式 |
| `IP_WHITELIST` | 空 | 逗号分隔，如 `192.168.1.0/24,10.0.0.5` |
| `SESSION_IDLE_MINUTES` | `30` | 会话空闲保活时长 |
| `RECORDING_MAX_MB` | `64` | 单次会话录像大小上限，0 为不限 |
| `UPLOAD_MAX_FILE_MB` | `4096` | 单文件上传上限 |

完整列表（含回收站、上传分片、凭证查看告警等）见 [deploy/README.md 第 4 节](deploy/README.md#4-环境变量)。

## 从源码构建

依赖 Go 1.22+ 与 Node.js 18+：

```bash
cd web && npm install && npm run build && cd ..   # 前端产物输出到 web/dist
go build -o shellcove ./cmd/shellcove                   # 前端由 go:embed 打进二进制
```

跨架构 / 发布包（`dist/` 下产出 amd64 与 arm64 的 tar.gz）：

```bash
bash deploy/build.sh
SKIP_WEB=1 bash deploy/build.sh                   # 复用已有的 web/dist，不联网装前端依赖
```

只想取二进制、不装 Go 与 Node：

```bash
docker build --target artifact --output type=local,dest=./dist .
```

## 技术栈

- **后端**：Go 1.22；`gorilla/websocket`、`pkg/sftp`、`golang-jwt/jwt/v5`、`pquerna/otp`、`modernc.org/sqlite`（纯 Go 实现，无需 CGO，产物全静态）
- **前端**：Vue 3 + Vite；终端基于 xterm.js（`addon-fit` / `addon-search` / `addon-web-links`）
- **存储**：SQLite 单文件
- **部署**：Docker 多阶段构建 / systemd，支持 amd64 与 arm64

## 项目结构

```
cmd/shellcove/        程序入口
internal/
  api/             HTTP / WebSocket 路由与中间件
  ssh/             SSH 会话管理与连接池
  sftp/            文件传输与分片续传
  sshinfo/         主机与凭证
  auth/            登录、JWT、TOTP
  permission/      资源级授权
  audit/           审计日志与会话录像
  backup/          备份导出与导入
  crypto/          AES-256-GCM 与主密钥
  db/              SQLite 与迁移
  config/          环境变量配置
  command/         命令片段
  monitor/         主机监控采集
  folder/          文件夹树
  trash/           回收站
  user/            用户
web/               Vue 3 前端源码与 go:embed 入口
docker/            容器入口脚本
deploy/            部署脚本与详细文档
Dockerfile         容器内构建前端 + 编译后端的多阶段构建
Dockerfile.runtime 免编译运行镜像（直接用预编译二进制）
```

## 安全说明

- **首次部署后请立即修改初始密码**（程序会强制），生产环境建议启用 TOTP 两步验证。
- 主密钥 `env.key` 必须与数据目录一起备份：**丢失后已保存的凭证与历史备份都无法恢复**，迁移主机时把整个数据目录搬走即可。
- 程序读取来源 IP 时直接信任 `X-Real-IP` / `X-Forwarded-For`，且没有可信代理校验。因此**不经反向代理直接暴露端口时，客户端可以伪造这两个头，从而绕过 `IP_WHITELIST`**、污染审计日志。请统一走 nginx（会覆盖这两个头，配置见 [deploy/README.md](deploy/README.md#5-反向代理与安全)）或用防火墙限制来源网段。
- 通过 http 访问时把 `SECURE_COOKIE` 设为 `false`；配置 HTTPS 后改回 `true`。
- 发现安全问题时请提交 Issue 或私下联系作者，请勿公开可直接利用的细节。

## 许可证

[Apache License 2.0](LICENSE)
