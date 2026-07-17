# DengDeng AI（蹬蹬ai）

面向团队和个人开发者的 AI API 网关：使用一把平台密钥统一接入 Claude、OpenAI 与 Gemini，并提供账号调度、故障切换、Token 级计费、充值与运营管理能力。

> 设计参考了 [sub2api](https://github.com/Wei-Shaw/sub2api) 的产品形态,但前后端均为独立实现:Go + Gin + GORM 后端,Vue 3 + Tailwind 自研前端。

## 功能

- **三平台兼容端点**:`/v1/messages`(Anthropic)、`/v1/chat/completions` `/v1/responses` `/v1/images/generations` `/v1/images/edits`(OpenAI)、`/v1beta/models/*`(Gemini),官方 SDK / Claude Code / Gemini CLI 只需改 Base URL + Key
- **分组账号池**:分组绑定平台与计费倍率,组内多账号按优先级调度,401/429/5xx 自动冷却并切换下一账号
- **流式透传 + 用量捕获**:SSE 原样转发,同时解析各平台 usage 字段(含缓存读写 token)
- **Token 级计费**:模型定价支持前缀通配(`claude-sonnet-*`)、图像 token 独立费率,用户倍率 × 分组倍率,余额微美元精度
- **用户体系**:注册/登录(JWT)、API Key 自助管理、用量明细、兑换码充值
- **管理端**:运营总览、用户管理(充值/封禁/倍率)、分组与上游账号、模型定价、兑换码批量生成、全站用量
- **在线支付**:多商户实例、EasyPay / 支付宝 / 微信支付 API v3 / Stripe / Airwallex，签名回调、精确金额核验、幂等入账、订单过期和退款冻结余额
- **OAuth 账号接入**:支持浏览器授权码 + PKCE 直接登录 Claude / OpenAI，OAuth 凭据自动加密保存并续期；也支持导入 sub2api、Codex `auth.json`、Claude Code credentials、CPA JSON
- **模型目录与别名**:管理端可配置对外模型名、上游映射和禁用状态；`GET /v1/models` 从本地启用目录返回，不会因账号池暂时为空而失败

## 技术栈

| 组件 | 技术 |
|------|------|
| 后端 | Go 1.25+ / Gin / GORM |
| 前端 | Vue 3 / Vite / TailwindCSS / Pinia |
| 存储 | SQLite(默认,零依赖)或 PostgreSQL |

## 文档

- [部署手册](docs/DEPLOYMENT.md)：Docker、二进制、Nginx、备份与升级
- [架构说明](docs/ARCHITECTURE.md)：请求链路、数据边界和关键模块
- [开发贡献](CONTRIBUTING.md)：本地环境、构建、测试与提交约定
- [安全说明](SECURITY.md)：凭据管理、漏洞反馈与上线检查

## 快速开始(本地开发)

```bash
# 1. 后端(端口 9100)
cd backend
JWT_SECRET=$(openssl rand -hex 32) ADMIN_PASSWORD=admin12345 go run ./cmd/server

# 2. 前端热更新(端口 5173,代理 /api 到 9100)
cd frontend
pnpm install
pnpm dev
```

打开 http://localhost:5173 ,用 `admin@dengdeng.local` / `admin12345` 登录。

生产构建(前端嵌入后端单二进制):

```bash
cd frontend && pnpm build          # 输出到 backend/internal/web/dist
cd ../backend && go build -o dengdeng ./cmd/server
```

## Docker 部署

```bash
cd deploy
cp .env.example .env
# 编辑 .env:填入 JWT_SECRET(openssl rand -hex 32)和 ADMIN_PASSWORD
docker compose up -d --build
```

默认监听 `127.0.0.1:9100`,SQLite 数据落在 `deploy/data/`,整个目录打包即可迁移。生产环境请用 Caddy/Nginx 反代并启用 HTTPS。

## 上线三步

1. **建分组**:管理端「分组管理」→ 新建,选平台(如 Claude)
2. **加账号**:「上游账号」→ 添加,填上游 API Key(Base URL 留空走官方)
3. **发密钥**:用户在「API 密钥」页自助创建 `dd-` 开头的密钥

### 直接登录 OAuth 上游账号

管理端「上游账号」→「添加账号」→ 选择 OAuth 后，可点击“去登录”完成浏览器授权。开发环境会自动启动 OpenAI 所需的本地 `localhost:1455`（端口被占用时 `1457`）回调桥接；生产部署请先在上游 OAuth 应用中登记下面的完整回调地址，并配置对应的 Client ID（如使用机密客户端还需 Client Secret）：

```yaml
oauth:
  openai:
    client_id: "..."
    client_secret: "..." # 可选，取决于上游 OAuth 应用类型
    redirect_url: "https://relay.example.com/api/admin/oauth/openai/callback"
  anthropic:
    client_id: "..."
    client_secret: "..."
    redirect_url: "https://relay.example.com/api/admin/oauth/anthropic/callback"
```

Docker 部署也可改用同名的 `OAUTH_OPENAI_*` / `OAUTH_ANTHROPIC_*` 环境变量。回调地址必须和上游应用注册值完全一致。

客户端接入示例:

```bash
# Claude Code
export ANTHROPIC_BASE_URL="https://your-domain.com"
export ANTHROPIC_AUTH_TOKEN="dd-xxx"

# OpenAI SDK
client = OpenAI(base_url="https://your-domain.com/v1", api_key="dd-xxx")

# Gemini
curl "https://your-domain.com/v1beta/models/gemini-2.5-pro:generateContent" \
  -H "x-goog-api-key: dd-xxx" -d '{...}'
```

### 上游代理与 OpenAI 生图

为访问受网络限制的上游，在部署配置中设置出站 HTTP(S) CONNECT 代理；它同时用于模型转发和 OAuth 刷新。`NO_PROXY` / `no_proxy` 可排除内网地址。

```yaml
proxy:
  url: "http://proxy.example:7890"
  no_proxy: "localhost,127.0.0.1,10.0.0.0/8"
```

OpenAI 生图直接把 SDK 的 base URL 指向本服务即可。推荐模型为 `gpt-image-2`，也支持 `POST /v1/images/edits` 的 multipart 图片编辑。服务会读取上游返回的图像 token usage，并使用管理端「模型定价」的图像输入/输出费率结算。

### 在线支付

先在部署配置设置 HTTPS 公网地址，支付中心才允许启用充值：

```yaml
site:
  public_url: "https://relay.example.com"
```

管理员随后在「支付中心」设置充值汇率（余额始终以微美元记账）并添加商户实例。渠道密钥以 AES-GCM 加密存储，管理端不会回显。回调地址由系统生成，分别为 `/api/payment/webhook/easypay`、`/alipay`、`/wxpay`、`/stripe` 与 `/airwallex`；请仅在对应商户后台登记 HTTPS 地址。

## 端到端测试

```bash
# 终端 1:mock 上游
cd backend && go run ./tools/mockupstream -port 9200

# 终端 2:主服务
JWT_SECRET=e2e-test-secret ADMIN_EMAIL=admin@test.local ADMIN_PASSWORD=admin12345 \
  DATABASE_PATH=/tmp/dd-test.db go run ./cmd/server

# 终端 3:跑测试(25 项断言)
./scripts/e2e.sh
```

## 项目结构

```
dengdeng/
├── backend/
│   ├── cmd/server/            # 入口
│   ├── internal/
│   │   ├── config/            # YAML + 环境变量配置
│   │   ├── model/             # GORM 数据模型
│   │   ├── store/             # 建库/迁移/初始化种子
│   │   ├── gateway/           # 中转核心:鉴权/转发/流式/用量提取
│   │   ├── service/           # 调度器/定价/计费
│   │   ├── handler/           # 控制台 API(auth/user/admin)
│   │   ├── middleware/        # JWT 鉴权
│   │   ├── server/            # 路由组装 + SPA 静态托管
│   │   └── web/               # 前端构建产物 embed
│   └── tools/mockupstream/    # 本地联调用 mock 上游
├── frontend/
│   └── src/
│       ├── api/               # fetch 封装 + 类型
│       ├── stores/            # Pinia (auth/toast)
│       ├── layouts/           # 控制台布局
│       ├── views/             # 用户端页面 + admin/ 管理端页面
│       └── components/        # 图表/表格/弹窗等
├── deploy/                    # docker-compose + 配置模板
├── scripts/e2e.sh             # 端到端测试
└── Dockerfile                 # 多阶段构建单镜像
```

## 安全设计

面向"防偷 token / 防偷账号 / 防破解"的实现:

- **上游 token 落库加密**:`UpstreamAccount.APIKey` 用 AES-256-GCM 加密存储(`crypto.EncryptedString` 透明加解密),即使数据库/备份泄漏也拿不到明文 key。主密钥来自 `ENCRYPTION_KEY`(未设置则从 `JWT_SECRET` 派生);兼容历史明文,下次写入自动升级为密文
- **平台密钥不可逆存储**:用户的 `dd-` 密钥只存 SHA-256,明文仅创建时返回一次
- **防撞库爆破**:登录/注册端点按 IP 限流(20 次/分钟),单邮箱连续 5 次失败锁定 15 分钟
- **会话可吊销**:JWT 带 `TokenVersion`,改密码 / 封禁 / 改角色后旧 token 立即失效(改密码会自动换发新 token,当前会话不掉线)
- **凭据隔离**:转发时客户端凭据与上游凭据严格分离,互不泄漏;跨平台密钥会被拒绝
- **加固响应头**:全站 `X-Frame-Options: DENY`、`X-Content-Type-Options: nosniff`、`Referrer-Policy`,控制台附加 CSP;控制台请求体限 1MB
- **其他**:密码 bcrypt;登录错误统一提示不暴露邮箱是否存在;GORM 全参数化无注入;`-s -w` 去符号

> 关于"防逆向":中转站是服务端 SaaS,后端二进制不对外分发,前端 JS 不含任何秘密(校验全在后端),所以代码混淆意义有限。真正的防线是上面的加密、限流、鉴权与 HTTPS。

生产部署务必:设置独立的 `ENCRYPTION_KEY` 与强 `JWT_SECRET`,启用 HTTPS 反代,数据库/`data` 目录定期加密备份。

后续可选增强(有产品权衡,按需开启):管理员/用户两步验证(TOTP)、操作审计日志、管理端 IP 白名单、单密钥用量突增告警。

## 计费口径

- 余额与费用以 **micro-USD**(百万分之一美元)整数存储,`1000000 = $1`
- 模型价格单位 **USD / 1M tokens**,费用 = 文本与图像各自 token × 对应单价 × 用户倍率 × 分组倍率
- 流式请求会在响应结束后按上游返回的 usage 结算;OpenAI 流式自动注入 `stream_options.include_usage` 保证拿到用量
- 余额 ≤ 0 时新请求返回 402

## 扩展路线

- 新增上游平台:`model/models.go` 加平台常量 → `gateway/routes.go` 加端点 → `gateway/usage.go` 加用量解析 → `gateway/gateway.go` 的 `forward()` 加认证头
- OAuth 订阅账号(Claude Pro / ChatGPT Plus 拼车):在 `UpstreamAccount` 上扩展 `auth_type` 与 token 刷新逻辑
- 在线支付:参考现有兑换码流程,新增支付回调 handler 后给用户 `balance_micro` 加账

## 免责声明

本项目仅供技术学习与研究。中转上游订阅/账号可能违反相应服务商的服务条款,请在合规前提下使用,风险自担。
