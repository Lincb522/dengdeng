<p align="center">
  <img src="frontend/public/brand/dengdeng-avatar.png" width="72" height="72" alt="DengDeng AI">
</p>

<h1 align="center">DengDeng AI · 蹬蹬ai</h1>

<p align="center">部署在自己服务器上的多模型 API 网关、账号池和计费后台。</p>

---

把各家模型接到业务里，麻烦通常不在一次请求，而在后面：密钥怎么发、上游账号怎么切、调用花了多少、出错后谁来排查。DengDeng 把这些放在一个入口里。客户端拿到一把 `dd-` 密钥；网关负责路由、账号调度、用量记录和结算。

它适合已有 OpenAI、Anthropic、Gemini 或 xAI 兼容接入的项目，也适合需要给团队成员、客户或不同业务线分配额度的场景。

## 先看它做什么

| 事情 | DengDeng 的处理方式 |
| --- | --- |
| 接入模型 | 保留 OpenAI、Anthropic、Gemini 兼容路径；xAI / Grok 使用 OpenAI 兼容路径 |
| 管理上游 | 按分组维护账号池，支持优先级、代理、冷却和故障切换 |
| 管理密钥 | 用户自助创建 `dd-` 密钥；可设置额度、失效时间、模型与 IP 规则 |
| 看用量 | 记录输入、输出、缓存 Token、图像用量、费用和请求状态 |
| 收款与运营 | 兑换码、在线充值、用户余额、模型定价、告警和运行监控 |

### 对外接口

| 协议 | 常用路径 |
| --- | --- |
| Anthropic | `/v1/messages` |
| OpenAI / xAI | `/v1/chat/completions`、`/v1/responses`、`/v1/images/generations`、`/v1/images/edits` |
| Gemini | `/v1beta/models/*` |

具体模型由管理端的模型目录和分组决定。`GET /v1/models` 直接读取本地启用目录，因此即使账号池临时不可用，客户端仍能正常拉取模型列表。

## 文档

- [部署手册](docs/DEPLOYMENT.md)：Docker、二进制、Nginx、备份和回滚
- [架构说明](docs/ARCHITECTURE.md)：请求如何经过网关、调度器和结算模块
- [更新记录](CHANGELOG.md)：重要功能、兼容性修复和升级说明
- [开发说明](CONTRIBUTING.md)：本地环境、测试和提交要求
- [安全说明](SECURITY.md)：凭据边界、上线检查和漏洞反馈

## 本地跑起来

需要 Go 1.25+、Node.js 22+ 和 pnpm。

```bash
# 终端 A：后端，默认监听 9100
cd backend
JWT_SECRET="$(openssl rand -hex 32)" \
ADMIN_PASSWORD=admin12345 \
go run ./cmd/server

# 终端 B：前端开发服务器，默认监听 5173
cd frontend
corepack enable
pnpm install
pnpm dev
```

打开 `http://localhost:5173`，使用 `admin@dengdeng.local` / `admin12345` 登录。仅限本地演示；任何可访问的环境都应设置独立的管理员密码和 `ENCRYPTION_KEY`。

生产二进制会把前端静态文件嵌入进去：

```bash
cd frontend
pnpm build

cd ../backend
go build -o dengdeng ./cmd/server
```

## 第一次配置

1. 在「分组管理」创建平台分组，例如 `openai-default` 或 `claude-team`。
2. 在「上游账号」把 API Key 或 OAuth 凭据加入对应分组；需要时为账号指定出站代理。
3. 在「模型配置」确认对外模型名、上游模型名和定价。
4. 用户在「API 密钥」创建 `dd-` 密钥后，即可将 Base URL 和密钥填入 SDK 或 CLI。

浏览器 OAuth 直连目前用于 Claude 和 OpenAI。生产环境需要先在上游登记回调地址，再填写对应的 `OAUTH_*` 配置；完整说明见 [部署手册](docs/DEPLOYMENT.md)。

### 客户端示例

```bash
# Claude Code
export ANTHROPIC_BASE_URL="https://your-domain.example"
export ANTHROPIC_AUTH_TOKEN="dd-xxx"

# Gemini
curl "https://your-domain.example/v1beta/models/gemini-2.5-pro:generateContent" \
  -H "x-goog-api-key: dd-xxx" \
  -H "content-type: application/json" \
  -d '{"contents":[{"parts":[{"text":"hello"}]}]}'
```

```python
# OpenAI SDK
from openai import OpenAI

client = OpenAI(
    base_url="https://your-domain.example/v1",
    api_key="dd-xxx",
)
```

## 部署

### Docker Compose

```bash
cd deploy
cp .env.example .env
# 填写 JWT_SECRET、ENCRYPTION_KEY、管理员账号和站点地址
docker compose up -d --build
curl -fsS http://127.0.0.1:9100/health
```

默认只监听 `127.0.0.1:9100`。对外服务时请用 Nginx 或 Caddy 提供 HTTPS；支付和 OAuth 回调依赖最终的 HTTPS 域名。

### 上游代理与图像请求

出站代理同时用于模型请求和 OAuth 刷新，可在部署配置中设置：

```yaml
proxy:
  url: "http://proxy.example:7890"
  no_proxy: "localhost,127.0.0.1,10.0.0.0/8"
```

OpenAI 兼容的图像生成和编辑路径可直接使用。图像模型可以单独指定上游分组和倍率，费用按管理端的图像定价结算。

## 结算怎么记

- 余额和费用以 micro-USD 整数保存，`1000000 = $1`。
- 模型价格单位为 USD / 1M Tokens；文本、缓存和图像分别计算，再叠加用户与分组倍率。
- 流式响应结束后根据上游返回的 usage 结算。OpenAI 流式请求会带上 `stream_options.include_usage`，以拿到完整用量。
- 余额不足时，新请求返回 `402`；已有流式响应不会被中途截断。

支付渠道、兑换码和人工加款都走同一套账单与幂等入账逻辑。支付回调必须配置 HTTPS 公网地址，且商户后台登记的回调地址应与 `SITE_PUBLIC_URL` 一致。

## 安全边界

- 平台 `dd-` 密钥只保存 SHA-256 摘要，明文仅在创建时显示一次。
- 上游 API Key 与 OAuth 凭据以 AES-256-GCM 加密保存；生产环境应设置独立的 `ENCRYPTION_KEY`。
- 登录和注册有 IP 限流与失败锁定；改密码、封禁或修改角色会让旧会话失效。
- 管理端和业务转发使用不同鉴权边界；仅已实现协议适配的跨平台调用可用，其余组合会被拒绝。
- 数据库、备份、证书、私钥和环境文件不应进入版本库。上线前请过一遍 [安全说明](SECURITY.md)。

## 测试

```bash
# 后端单元测试
cd backend
go test ./...

# 端到端测试需要两个终端
go run ./tools/mockupstream -port 9200

JWT_SECRET=e2e-test-secret \
ADMIN_EMAIL=admin@test.local \
ADMIN_PASSWORD=admin12345 \
DATABASE_PATH=/tmp/dd-test.db \
go run ./cmd/server

# 另一个终端
./scripts/e2e.sh
```

## 目录

```text
dengdeng/
├── backend/       Go 服务、网关、调度、计费和管理 API
├── frontend/      Vue 控制台
├── deploy/        Docker Compose、systemd、Nginx 配置模板
├── docs/          架构与部署文档
└── scripts/       本地演示和端到端测试
```

## 使用说明

项目可以用于自建网关和内部服务。接入上游账号、订阅或模型服务前，请自行确认使用方式符合对应平台的服务条款和当地法律要求。
