# 贡献指南

感谢你有兴趣参与 ShellCove。本文档说明如何搭建开发环境，以及提交代码时遵循的约定。

## 开发环境

| 依赖 | 版本 |
| --- | --- |
| Go | 1.22+ |
| Node.js | 18+ |
| Docker | 可选，用于容器化验证 |

## 本地运行

```bash
# 1. 构建前端（产物输出到 web/dist，会被 go:embed 打进二进制）
cd web && npm install && npm run build && cd ..

# 2. 启动（http 本地调试）
DATA_DIR=./data \
ENCRYPTION_KEY_FILE=./data/env.key \
SECURE_COOKIE=false \
go run ./cmd/shellcove
```

首次启动会在日志中打印初始密码。

> `SECURE_COOKIE=false` 是本地 http 访问的必要条件，否则登录后 Cookie 不会带上。

只改后端时，前端产物若已存在可跳过第一步，直接 `go run ./cmd/shellcove`。

## 目录约定

- `internal/` 按领域分包，请避免引入循环依赖
- `web/` 为 Vue 3 前端，所有后端调用集中在 `web/src/api.js`
- `deploy/` 放部署脚本，详细运维文档在 `deploy/README.md`
- 不要提交 `dist/`、`data/`、`node_modules/`（已在 `.gitignore` 中排除）

## 提交前自检

```bash
gofmt -l .          # 应无输出
go vet ./...
go build ./...
cd web && npm run build
```

## 提交信息

采用 [Conventional Commits](https://www.conventionalcommits.org/zh-hans/)：

```
feat(terminal): 支持在会话内搜索
fix(sftp): 修复断网后大文件分片不再重试
docs: 补充反向代理配置示例
```

常用类型：`feat` `fix` `docs` `refactor` `perf` `test` `chore`。

## 提交 PR

1. Fork 后从 `main` 切出分支，命名如 `feat/xxx`、`fix/xxx`
2. 一个 PR 只做一件事，并在描述中说明动机与验证方式
3. 界面改动请附截图或录屏
4. 涉及权限、认证、加解密的改动，请明确说明影响面与兼容性

## 报告问题

- 功能缺陷与使用问题：使用 Issue 模板提交
- 安全漏洞：**不要开公开 Issue**，请见 [SECURITY.md](SECURITY.md)
