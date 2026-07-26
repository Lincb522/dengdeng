<p align="center">
  <img src="frontend/public/brand/dengdeng-avatar.png" width="88" height="88" alt="DengDeng AI 图标">
</p>

<h1 align="center">DengDeng AI · 蹬蹬ai</h1>

<p align="center">
  自托管的多模型 API 网关、上游账号池、计费系统与运营控制台
</p>

<p align="center">
  <a href="https://github.com/Lincb522/dengdeng/actions"><img src="https://img.shields.io/github/actions/workflow/status/Lincb522/dengdeng/publish-container.yml?branch=main&label=build" alt="构建状态"></a>
  <a href="https://github.com/Lincb522/dengdeng/pkgs/container/dengdeng"><img src="https://img.shields.io/badge/GHCR-amd64%20%7C%20arm64-2f6feb" alt="容器镜像"></a>
  <img src="https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white" alt="Go 1.25+">
  <img src="https://img.shields.io/badge/Vue-3.5+-42b883?logo=vuedotjs&logoColor=white" alt="Vue 3.5+">
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-LGPL--3.0--or--later-c98a20" alt="LGPL-3.0-or-later"></a>
</p>

<p align="center">
  <a href="https://dengdeng.ganiran.com">站点</a>
  · <a href="docs/DEPLOYMENT.md">部署手册</a>
  · <a href="docs/ARCHITECTURE.md">架构说明</a>
  · <a href="CHANGELOG.md">更新日志</a>
  · <a href="SECURITY.md">安全说明</a>
</p>

---

DengDeng AI 把模型接入、上游账号、用户密钥、用量计费、充值结算和运行监控放在同一套系统中。调用方只需要一把 `dd-` 密钥；网关负责识别协议和模型、选择可用分组与上游账号、转发流式响应、记录 Token 和缓存用量，并按配置完成扣费。

它可以部署在个人服务器、团队内网或独立运营环境中。项目同时提供用户端和管理端：用户管理自己的密钥、额度、用量和钱包；管理员维护上游账号池、模型目录、定价、支付、告警、备份与系统策略。

> 本项目不会附带任何模型账号、订阅额度或第三方服务授权。部署者和使用者必须自行确认上游账号来源、平台条款、当地法律、支付资质和数据处理要求。

## 目录

- [主要能力](#主要能力)
- [支持的接口](#支持的接口)
- [请求如何工作](#请求如何工作)
- [快速开始](#快速开始)
- [首次配置](#首次配置)
- [客户端接入](#客户端接入)
- [计费与倍率](#计费与倍率)
- [生产部署](#生产部署)
- [安全与数据](#安全与数据)
- [开发与测试](#开发与测试)
- [常见问题](#常见问题)
- [用户协议说明](#用户协议说明)
- [免责声明](#免责声明)
- [开源协议](#开源协议)
- [参考与致谢](#参考与致谢)

## 主要能力

### 网关与协议

- 提供 OpenAI Chat Completions、OpenAI Responses、Anthropic Messages 和 Gemini 兼容入口。
- 支持流式 SSE、工具调用、结构化内容、Token 计数、Responses compact 与部分跨协议转换。
- 支持 OpenAI 兼容的图像生成、图像编辑和异步图像任务。
- 根据入站端点、模型目录和分组平台决定目标上游，不允许未实现的跨平台组合静默通过。
- `GET /v1/models` 读取本地启用的模型目录，账号池短暂不可用时仍可向客户端返回稳定模型列表。

### 上游账号池

- 支持 API Key、OAuth、PAT 与 OpenAI Agent Identity 等凭证类型。
- 支持 Sub2API JSON、CPA 类凭证、单对象、数组与 JSONL 等导入方式。
- 账号可设置分组、并发、优先级、自定义排序、代理和可用状态。
- 调度器根据平台、模型、额度、并发、优先级、冷却和健康状态选择账号。
- 请求失败时可在同一分组切换账号，并在同平台已选分组之间继续故障转移。
- 自动刷新 OAuth 与配额信息；API Key 上游可查询第三方中转的余额和额度。

### 密钥与用户

- 用户自助创建 `dd-` API 密钥，服务端只保存 SHA-256 摘要。
- 一把密钥可绑定一个或多个分组；多分组能力可以在系统设置中关闭。
- 每把密钥可独立配置总额度、每日额度、RPM、并发、到期时间、IP 白名单和黑名单。
- 支持密钥复制、显隐、轮换，以及 Claude Code、Codex CLI、Gemini CLI、Chatbox、CCSwitch 等快速配置。
- 支持邮箱验证码注册、密码重置、OAuth 登录、TOTP、会话失效和管理员权限控制。

### 用量、计费与运营

- 记录输入 Token、输出 Token、缓存创建、缓存读取、图像用量、请求次数和最终费用。
- 记录分组、模型、用户、密钥、上游账号、入站端点、请求 IP、地区、首字耗时、总耗时和排队耗时。
- 支持按量、按次、思考强度倍率、用户倍率、分组倍率和缓存倍率。
- 提供用户用量明细、全站用量、模型与账号排行、实时运行指标和服务器状态监控。
- 支持余额、充值、兑换码、订单、账单、推广码、推广分成与现金分账记录。
- 支持渠道探测、额度刷新、告警、审计日志、自动备份、旧数据清理和版本更新。

### 控制台

| 用户端 | 管理端 |
| --- | --- |
| 总览、模型广场、图像创作 | 运营总览、运行监控、告警巡检 |
| API 密钥、快速配置 | 分组、上游账号、代理、模型与定价 |
| 我的用量、钱包与充值 | 用户、兑换码、推广分成与支付中心 |
| 推广中心、账户安全 | 系统设置、数据库备份与版本更新 |

控制台提供暖色浅色主题和高对比度深色主题，并针对桌面、平板与手机重新组织表格、卡片和弹窗。

## 支持的接口

### 公共模型接口

| 协议 | 方法与路径 | 用途 |
| --- | --- | --- |
| OpenAI | `GET /v1/models` | 获取当前密钥可见模型 |
| OpenAI | `POST /v1/chat/completions` | Chat Completions |
| OpenAI | `POST /v1/responses` | Responses API |
| OpenAI | `POST /v1/responses/compact` | 压缩上下文 |
| OpenAI | `POST /v1/responses/input_tokens` | 估算输入 Token |
| Anthropic | `POST /v1/messages` | Messages API |
| Anthropic | `POST /v1/messages/count_tokens` | Token 计数 |
| Gemini | `POST /v1beta/models/:model:generateContent` | Gemini 内容生成 |
| Gemini | `POST /v1beta/models/:model:streamGenerateContent` | Gemini 流式生成 |
| 图像 | `POST /v1/images/generations` | 同步图像生成 |
| 图像 | `POST /v1/images/edits` | 图像编辑 |
| 图像 | `POST /v1/images/generations/async` | 创建异步图像任务 |
| 图像 | `GET /v1/images/tasks/:task_id` | 查询异步任务 |
| 用量 | `GET /v1/usage` | 查询密钥余额与额度 |

兼容范围以代码和实际模型配置为准。部分客户端会自动拼接 `/v1`，配置 Base URL 时应按客户端要求填写，避免出现 `/v1/v1/...`。

### 常见状态码

| 状态码 | 常见原因 |
| --- | --- |
| `400` | 参数格式或模型参数不兼容 |
| `401` | `dd-` 密钥无效、停用或未正确传入 |
| `402` | 用户或密钥余额不足 |
| `403` | IP 规则、权限、分组或上游访问限制 |
| `413` | 请求正文超过反向代理或服务限制 |
| `429` | 用户、密钥或账号并发已满，或触发上游限流 |
| `503` | 当前模型对应分组没有可用上游账号 |

## 请求如何工作

```text
SDK / CLI / 第三方客户端
          │
          │  dd- API Key
          ▼
协议入口与密钥鉴权
          │
          ├─ 模型映射与参数归一化
          ├─ 用户 / 密钥并发槽
          ├─ 分组选择与账号调度
          └─ 可选出站代理
          │
          ▼
OpenAI / Anthropic / Gemini / xAI / 第三方中转
          │
          ▼
流式响应、usage 提取、计费、日志与监控
```

完整链路：

1. 网关从 `Authorization: Bearer dd-...`、`x-api-key` 或兼容头中读取平台密钥。
2. 校验密钥状态、到期时间、IP 规则、用户余额、密钥额度、RPM 和并发。
3. 根据端点、模型目录和密钥已选分组确定目标平台及上游模型。
4. 请求占用用户和密钥并发槽；调度器再选择并占用一个可用上游账号。
5. 请求通过账号指定代理或全局网络配置发送到上游，流式事件按目标协议返回。
6. 服务提取输入、输出、缓存和图像用量，计算费用，写入账单、用量明细和运行指标。
7. 可重试错误会按策略切换账号或分组；客户端断开、失败和流结束都会释放并发槽。

## 快速开始

### 环境要求

| 组件 | 建议版本 |
| --- | --- |
| Go | 1.25.6 或更高 |
| Node.js | 22 或更高 |
| pnpm | 11.14 或兼容版本 |
| 数据库 | SQLite，或 PostgreSQL 15+ |
| 反向代理 | Nginx、Caddy 或同类 HTTPS 网关 |

### 方式一：Docker Compose

```bash
git clone https://github.com/Lincb522/dengdeng.git
cd dengdeng/deploy

cp .env.example .env
openssl rand -hex 32
```

编辑 `.env`，至少填写：

```dotenv
JWT_SECRET=替换为随机值
ENCRYPTION_KEY=替换为另一组随机值
ADMIN_EMAIL=admin@example.com
ADMIN_PASSWORD=替换为强密码
SITE_NAME=DengDeng AI
SITE_PUBLIC_URL=https://your-domain.example
```

启动并检查：

```bash
docker compose pull
docker compose up -d
docker compose logs -f dengdeng
curl -fsS http://127.0.0.1:9100/health
```

默认端口只绑定 `127.0.0.1:9100`。对外使用前必须配置 HTTPS 反向代理。

### 方式二：本地开发

终端 A：

```bash
cd backend

JWT_SECRET="$(openssl rand -hex 32)" \
ENCRYPTION_KEY="$(openssl rand -hex 32)" \
ADMIN_EMAIL=admin@dengdeng.local \
ADMIN_PASSWORD=admin12345 \
go run ./cmd/server
```

终端 B：

```bash
cd frontend
corepack enable
pnpm install
pnpm dev
```

打开 `http://127.0.0.1:5173`，使用上面设置的管理员账号登录。`admin12345` 只用于本机开发，不能用于任何可被外部访问的环境。

### 方式三：构建单文件服务

前端产物会写入后端的嵌入目录：

```bash
cd frontend
pnpm install --frozen-lockfile
pnpm build

cd ../backend
go build -trimpath -o dengdeng ./cmd/server
./dengdeng
```

## 首次配置

建议按以下顺序完成：

1. 在“系统设置”填写站点名称、公开 HTTPS 地址、注册策略、SMTP 和安全选项。
2. 在“分组管理”创建平台分组，例如 `openai-default`、`claude-team` 或 `gemini-main`。
3. 在“代理配置”添加确有需要的出站代理；不需要代理时保持为空。
4. 在“上游账号”添加 API Key、OAuth、PAT 或 Agent Identity，并绑定正确分组。
5. 在“模型配置”检查对外模型名、上游模型名、平台、上下文和最大输出。
6. 在“模型定价”设置文本、缓存、图像、按次价格和思考强度倍率。
7. 使用账号测试、模型列表和短对话验证上游，再向用户开放分组。
8. 创建测试用户和 `dd-` 密钥，确认模型获取、对话、用量、扣费和告警均正常。
9. 配置数据库自动备份并手动执行一次恢复演练。

### 上游额度查询

OAuth 和 Agent Identity 会显示可读取的订阅周期、窗口额度和重置时间。API Key 类型的上游会尝试查询所接第三方中转站的余额与额度，内置兼容 DengDeng、Sub2API、New API 和 One API 常见格式。非标准中转可配置自定义余额查询地址。

查询失败不等同于账号一定不可用：部分上游不提供余额接口，此时系统会显示连接状态、真实请求反馈和本站统计用量。最终可用性仍应通过账号测试和实际模型请求判断。

### OpenAI Agent Identity

Agent Identity 与普通 ChatGPT OAuth、Codex PAT 和 CPA OAuth 文件不是同一种凭证。导入前请阅读 [Agent Identity 说明](docs/AGENT_IDENTITY.md)。系统支持 Codex Access Token 登录后生成的 `auth.json`，并加密保存运行时身份和签名私钥；不会把普通 OAuth 文件自动当成 Agent Identity。

## 客户端接入

以下示例将 `https://your-domain.example` 替换成实际域名，将 `dd-xxx` 替换成平台密钥。

### OpenAI SDK

```python
from openai import OpenAI

client = OpenAI(
    base_url="https://your-domain.example/v1",
    api_key="dd-xxx",
)

response = client.responses.create(
    model="your-openai-model-id",
    input="你好",
)

print(response.output_text)
```

### curl

```bash
curl https://your-domain.example/v1/responses \
  -H 'Authorization: Bearer dd-xxx' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "your-openai-model-id",
    "input": "你好"
  }'
```

### Claude Code

```bash
export ANTHROPIC_BASE_URL="https://your-domain.example"
export ANTHROPIC_AUTH_TOKEN="dd-xxx"
export CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
export ANTHROPIC_MODEL="your-claude-model-id"

claude
```

OpenAI / Codex 分组也可以通过兼容层接入 Claude Code，但工具调用、提示词缓存和流式事件必须先在实际业务中测试。

### Codex CLI

`~/.codex/config.toml`：

```toml
model_provider = "dengdeng"
model = "your-openai-model-id"
review_model = "your-openai-model-id"
cli_auth_credentials_store = "file"

[model_providers.dengdeng]
name = "DengDeng AI"
base_url = "https://your-domain.example/v1"
wire_api = "responses"
requires_openai_auth = true
```

`~/.codex/auth.json`：

```json
{
  "OPENAI_API_KEY": "dd-xxx"
}
```

### Gemini

```bash
curl 'https://your-domain.example/v1beta/models/your-gemini-model-id:generateContent' \
  -H 'x-goog-api-key: dd-xxx' \
  -H 'Content-Type: application/json' \
  -d '{"contents":[{"parts":[{"text":"你好"}]}]}'
```

### 图像生成

```bash
curl https://your-domain.example/v1/images/generations \
  -H 'Authorization: Bearer dd-xxx' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "your-image-model-id",
    "prompt": "暖色纸张质感的极简海报",
    "size": "1024x1024"
  }'
```

用户端“API 密钥 → 快速配置”可以直接生成 Claude Code、Codex CLI、Gemini CLI、Chatbox、Cherry Studio、NextChat、Cline、Continue、OpenCode 和 CCSwitch 配置，并提供对应工具下载入口。

## 计费与倍率

### 基础单位

- 所有余额和费用以 micro-USD 整数保存，`1,000,000 micro-USD = 1 USD`。
- 文本模型价格通常以 USD / 1M Tokens 配置。
- 图像模型可以按张数、尺寸、质量或请求次数配置。
- 按次分组可直接按成功请求次数扣费，不依赖 Token 总量。

### 文本请求

费用由以下部分组成：

```text
输入费用   = 输入 Token × 输入单价
输出费用   = 输出 Token × 输出单价
缓存费用   = 缓存创建 / 缓存读取 Token × 对应单价
基础费用   = 输入费用 + 输出费用 + 缓存费用
最终费用   = 基础费用 × 分组倍率 × 用户倍率 × 思考强度倍率
```

具体实现会根据平台返回的 usage 字段和当前模型计价规则处理。不同上游对系统提示、工具定义、推理 Token 和缓存的统计口径可能不同，因此客户端本地估算不一定与最终账单完全一致。

### 余额与支付

- 用户余额、兑换码、在线充值和人工加款均写入统一账本。
- 支付订单使用幂等入账，重复回调不会重复增加余额。
- 支付回调依赖公网 HTTPS 地址，商户后台回调域名应与 `SITE_PUBLIC_URL` 一致。
- 已支付未到账时应先核对订单状态、支付回调日志和签名配置，再进行人工补单。
- 生产运营支付功能前，应自行确认商户资质、税务、退款和消费者保护要求。

## 生产部署

### 容器镜像

镜像发布到：

```text
ghcr.io/lincb522/dengdeng
```

标签说明：

| 标签 | 含义 |
| --- | --- |
| `latest` | 最新正式版本 |
| `edge` | 跟随 `main` 的开发构建 |
| `0.1.0` | 固定正式版本 |
| `sha-xxxxxxx` | 固定到具体提交 |

生产环境应固定版本标签或摘要，不建议长期直接使用 `edge`。

### Nginx 要点

- 对外仅开放 HTTPS，后端继续监听 `127.0.0.1:9100`。
- 为 `/v1/responses`、`/v1/messages` 和图像上传设置合理的 `client_max_body_size`。
- 流式接口关闭代理缓冲，并设置足够长但有上限的读取超时。
- 只信任实际连接到源站的 CDN 或反向代理地址，避免伪造 `X-Forwarded-For`。
- 不要把数据库、备份目录、配置文件或更新脚本暴露为静态文件。

仓库提供 Nginx、systemd 和更新脚本模板，见 [deploy](deploy) 与 [部署手册](docs/DEPLOYMENT.md)。

### 备份与更新

- SQLite 数据库默认位于持久化数据目录，容器升级前必须确认目录已挂载。
- 自动备份支持间隔、保留天数和保留数量，建议同时保留异机备份。
- 恢复数据库前先停止写入流量，并校验备份文件可读。
- 热更新会先拉取受信任仓库、完成构建和健康检查，再切换运行版本。
- 每次发布前应更新 [CHANGELOG.md](CHANGELOG.md)，生产升级前先阅读兼容性和迁移说明。

## 安全与数据

### 必须保护的内容

- `JWT_SECRET`、`ENCRYPTION_KEY`、管理员密码、SMTP 密码和 OAuth Client Secret。
- 上游 API Key、Refresh Token、PAT、Agent Identity 私钥和代理认证信息。
- 数据库、备份、支付私钥、证书私钥、环境文件与服务日志。

### 上线检查

- 使用两组独立随机值设置 `JWT_SECRET` 和 `ENCRYPTION_KEY`，并离线备份。
- 修改管理员默认账号和密码，启用邮箱验证，按需要启用 TOTP。
- 只允许 HTTPS；限制源站端口和管理端访问来源。
- 设置可信代理与真实客户端 IP 头，确认日志显示的是客户端地址而不是内网代理地址。
- 为密钥设置最小必要分组、额度、并发和 IP 规则。
- 对支付、OAuth、邮件、对象存储和更新功能分别进行最小权限配置。
- 定期检查审计日志、异常请求、上游 401 / 403 / 429、低余额和备份结果。

漏洞反馈和安全边界见 [SECURITY.md](SECURITY.md)。请勿在公开 Issue 中提交真实密钥、Token、数据库、支付信息或用户数据。

## 开发与测试

### 项目结构

```text
dengdeng/
├── backend/
│   ├── cmd/server/          服务启动入口
│   ├── internal/config/     YAML 与环境变量配置
│   ├── internal/gateway/    协议适配、转发、流式与用量提取
│   ├── internal/handler/    用户端与管理端 HTTP API
│   ├── internal/model/      GORM 数据模型
│   ├── internal/service/    调度、计费、支付、邮件、告警与备份
│   ├── internal/store/      数据库连接与迁移
│   └── internal/web/        嵌入的前端产物
├── frontend/
│   ├── public/              品牌与静态资源
│   └── src/                 Vue 页面、组件、状态与 API 客户端
├── deploy/                  Docker、Nginx、systemd 与更新脚本
├── docs/                    架构、部署和功能说明
├── scripts/                 端到端与辅助脚本
└── .github/workflows/       镜像发布流水线
```

### 检查命令

```bash
# 前端构建
cd frontend
pnpm build

# 后端测试
cd ../backend
go test ./...

# 格式与静态检查
go fmt ./...
go vet ./...
```

### 端到端测试

```bash
cd backend
go run ./tools/mockupstream -port 9200
```

另一个终端启动测试服务：

```bash
cd backend
JWT_SECRET=e2e-test-secret \
ADMIN_EMAIL=admin@test.local \
ADMIN_PASSWORD=admin12345 \
DATABASE_PATH=/tmp/dd-test.db \
go run ./cmd/server
```

然后执行：

```bash
./scripts/e2e.sh
```

提交代码前请阅读 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 常见问题

### Chatbox 能获取模型，但对话显示 Network Error

检查 Base URL 是否为完整 HTTPS 地址、证书链是否正常、CORS 是否允许当前来源，以及客户端是否自动追加 `/v1`。同时查看浏览器控制台、Nginx 错误日志和后端请求日志。

### 返回 `503 no available upstream account in this group`

依次确认：

1. 密钥绑定了正确平台的分组。
2. 分组已对用户开放且包含目标模型。
3. 至少一个上游账号处于启用、未冷却、未过期且有可用并发的状态。
4. 上游凭据没有返回 401 / 403，订阅或余额没有失效。
5. 代理和目标上游网络可达。

### HTTP 200，但客户端看不到回复

通常是流式事件或跨协议字段不兼容。检查后端是否收到有效 `output_text`、Anthropic `content_block_delta` 或 Chat Completions `delta.content`，并确认反向代理没有缓冲或改写 SSE。再用 `curl` 直接请求同一端点，排除客户端解析问题。

### 返回 413

提高 Nginx 的 `client_max_body_size`，并确认 CDN、WAF 和上游限制。不要无限提高限制；长上下文和内联图片会显著增加内存、带宽和费用。

### 支付成功但余额未增加

检查订单号、支付渠道状态、回调地址、签名、服务器时间和入账日志。人工核验必须以支付渠道真实订单为依据，不能只根据前端成功页补款。

### 快速配置刷新后要求重新生成密钥

服务端只保存密钥摘要，无法恢复明文。新创建或主动补入的密钥可以选择保存在当前浏览器本机；清理浏览器数据、使用无痕窗口或更换设备后，需要重新补入原密钥或轮换密钥。

## 用户协议说明

项目内置以下站点文档模板：

1. 用户协议
2. 隐私政策
3. 可接受使用政策
4. 服务特定条款
5. 免责声明
6. 开源软件说明

模板覆盖账户与密钥、余额与计费、第三方上游、数据处理、禁止行为、模型输出风险和开源软件归属。管理员可在“系统设置 → 登录与协议”中修改内容、更新日期、排序和展示方式。

这些文本是项目默认模板，不是针对你的主体、地区和业务出具的法律意见。公开运营前应由具备资质的专业人士结合主体名称、注册地址、支付模式、退款政策、数据存储位置、未成年人规则和当地法律进行审阅。部署者修改模板后，应确保登录页、充值页、隐私实践和实际系统行为一致。

## 免责声明

本软件和默认站点按“现状”和“可用”基础提供，不保证任何上游账号、订阅、模型、协议转换、网络链路、支付渠道或第三方客户端持续可用，也不保证模型输出准确、完整或适合特定目的。

使用本项目可能受到 OpenAI、Anthropic、Google、xAI、支付机构、云服务商和其他第三方的服务条款、地区限制、风控与许可证约束。部署者和使用者应自行确认授权来源与合规性，并承担因账号限制、上游变更、输出错误、密钥泄露、费用超支、数据丢失、业务中断或违法使用产生的责任。

本项目不提供法律、医疗、金融、投资、税务或其他专业意见。任何高风险用途都必须进行独立验证、专业审核和人工决策。开源项目维护者不参与各个独立部署实例的充值、交易、数据处理与客户服务，也不为第三方部署提供默认担保。

完整站点免责声明位于默认协议文档中；适用法律不得排除的责任不受本段影响。

## 开源协议

DengDeng AI 依据 [GNU Lesser General Public License v3.0 或更高版本](LICENSE) 发布，即 `LGPL-3.0-or-later`。

你可以在许可证范围内使用、研究、修改和分发本项目。分发修改版本、二进制或组合程序时，必须保留版权和许可证通知，并按 LGPL-3.0-or-later 及其中引用的 GNU GPL 条款提供对应源码和允许修改、重新链接所需的信息。许可证原文与本说明不一致时，以 [LICENSE](LICENSE) 为准。

开源许可证只适用于仓库中由本项目授权的代码和资源，不自动包含：

- DengDeng AI / 蹬蹬ai 的商标、域名和商业运营权；
- 上游账号、订阅额度、API Key、OAuth 凭据和代理资源；
- 用户数据、账单、数据库、支付配置和部署密钥；
- 具有独立许可证的第三方代码、字体、图标和依赖。

第三方通知见 [NOTICE.md](NOTICE.md)。

## 参考与致谢

DengDeng AI 的实现和兼容性工作受以下项目启发。感谢这些项目的作者、维护者和贡献者。

| 项目 | 在 DengDeng AI 中的参考范围 |
| --- | --- |
| [Sub2API](https://github.com/Wei-Shaw/sub2api) | 账号池、调度、计费、运营监控、Agent Identity 与网关能力 |
| [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) | Codex / Claude 客户端协议、OAuth / PAT 与 Responses 兼容行为 |
| [New API](https://github.com/QuantumNous/new-api) | 第三方中转接口、模型与额度查询兼容思路 |
| [One API](https://github.com/songquanpeng/one-api) | OpenAI 兼容中转、渠道和额度接口生态 |
| [CCSwitch](https://github.com/farion1231/cc-switch) | CLI 服务商切换、快速导入和用量查询配置 |
| [Aside Music](https://github.com/Lincb522/Aside-music) | 早期站点布局、暖色视觉语言和品牌页面参考 |

核心技术栈同样离不开 [Go](https://go.dev/)、[Gin](https://gin-gonic.com/)、[GORM](https://gorm.io/)、[Vue](https://vuejs.org/)、[Vite](https://vite.dev/)、[Pinia](https://pinia.vuejs.org/)、[Tailwind CSS](https://tailwindcss.com/)、[SQLite](https://sqlite.org/)、[PostgreSQL](https://www.postgresql.org/) 与 [AWS SDK for Go](https://aws.github.io/aws-sdk-go-v2/) 等开源软件。

本项目不是上述第三方项目的官方分支，兼容、引用和致谢不代表对方为 DengDeng AI 提供背书、担保或商业合作承诺。使用任何第三方代码或服务时，请同时遵守其原始许可证和服务条款。

## 联系与反馈

- GitHub Issues：用于可复现的缺陷和功能建议，请勿粘贴真实密钥或用户数据。
- 安全问题：按照 [SECURITY.md](SECURITY.md) 中的方式私下报告。
- QQ 交流群：`1072353908`。

<p align="center">
  <sub>部署前先读协议，升级前先做备份，公开日志前先脱敏。</sub>
</p>
