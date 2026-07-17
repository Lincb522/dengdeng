# 开发与贡献

这里放的是可复现的代码和配置模板，不是线上环境的备份。提交前请确认改动能构建、能测试，也没有真实凭据或用户数据。

## 本地环境

- Go 1.25+
- Node.js 22+
- pnpm（通过 Corepack 管理）
- 可选：Docker、SQLite 命令行工具

```bash
corepack enable
cd frontend && pnpm install

# 终端 A：后端
cd backend
JWT_SECRET="$(openssl rand -hex 32)" \
ADMIN_EMAIL=admin@dengdeng.local \
ADMIN_PASSWORD=admin12345 \
go run ./cmd/server

# 终端 B：前端开发服务器
cd frontend
pnpm dev
```

前端开发地址为 `http://localhost:5173`，后端健康检查为 `http://127.0.0.1:9100/health`。

## 检查与测试

后端改动至少执行：

```bash
cd backend
go test ./...
go vet ./...
```

前端改动至少执行：

```bash
cd frontend
pnpm build
```

端到端测试需要两个服务：主服务监听 `9100`，Mock 上游监听 `9200`。完整步骤见根目录 [README](README.md#端到端测试)。

## 提交时注意

1. 一个提交只做一件能说清楚的事；例如 `fix: handle empty usage response`。
2. 不提交 `.env`、`config.yaml`、SQLite 数据库、证书、私钥、上游账号凭据、支付密钥或 `dd-` 密钥。
3. 更新接口、部署方式或用户可见行为时，同步更新 README 或 `docs/`。
4. 不手工提交 `frontend/node_modules`、本地二进制和前端构建产物；生产镜像会在构建时生成它们。

## 配置边界

`deploy/.env.example` 与 `deploy/config.example.yaml` 是唯一应被修改后复制的配置模板。真实配置应位于服务器受限目录或部署平台的 Secret 管理中，且文件权限应限制为运行账户可读。
