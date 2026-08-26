<p align="center">
  <img src="frontend/public/brand/dengdeng-avatar.png" width="88" height="88" alt="DengDeng AI 图标">
</p>

<h1 align="center">DengDeng AI · 蹬蹬ai</h1>

<p align="center">自托管的多模型 API 网关、账号池、计费系统与运营控制台</p>

<p align="center">
  <a href="https://github.com/Lincb522/dengdeng/actions/workflows/quality.yml"><img src="https://img.shields.io/github/actions/workflow/status/Lincb522/dengdeng/quality.yml?branch=main&label=quality" alt="质量检查"></a>
  <a href="https://github.com/Lincb522/dengdeng/pkgs/container/dengdeng"><img src="https://img.shields.io/badge/GHCR-amd64%20%7C%20arm64-2f6feb" alt="容器镜像"></a>
  <img src="https://img.shields.io/badge/Go-1.26.5-00ADD8?logo=go&logoColor=white" alt="Go 1.26.5">
  <img src="https://img.shields.io/badge/Node.js-26-5FA04E?logo=nodedotjs&logoColor=white" alt="Node.js 26">
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-LGPL--3.0--or--later-c98a20" alt="LGPL-3.0-or-later"></a>
</p>

<p align="center">
  <a href="https://dengdeng.ganiran.com">在线站点</a>
  · <a href="docs/DEPLOYMENT.md">部署</a>
  · <a href="docs/API.md">API</a>
  · <a href="docs/ARCHITECTURE.md">架构</a>
  · <a href="CHANGELOG.md">更新记录</a>
</p>

---

DengDeng AI 为 OpenAI、Anthropic、Gemini、xAI 及兼容上游提供统一入口。调用方使用一把 `dd-` 密钥；网关负责模型与协议匹配、账号调度、流式转发、用量采集和费用结算。用户端提供密钥、用量、充值、渠道状态与能力库，管理端负责账号池、分组、模型、价格、支付、监控、告警、备份和运行策略。

本项目不附带模型账号、订阅额度、支付资质或第三方服务授权。部署者必须自行确认上游账号来源、平台条款、数据处理要求和当地法律。

## 能力范围

| 领域 | 已实现能力 |
| --- | --- |
| API 网关 | OpenAI Chat Completions、Responses、Anthropic Messages、Gemini `v1beta`、图像、xAI 视频/音频/搜索；支持 JSON、SSE 和部分跨协议转换 |
| 上游平台 | OpenAI、Anthropic、Gemini、xAI/Grok、Kimi、智谱 GLM、DeepSeek 与复合兼容账号 |
| 凭证 | API Key、OAuth、PAT、Codex Agent Identity；支持 Sub2API/CPA 类 JSON、数组和 JSONL 导入 |
| 调度 | 账号多分组、优先级、自定义顺序、并发、额度、冷却、模型级故障隔离、同平台分组故障转移和独立代理 |
| 用户密钥 | 多分组路由、可恢复密文、轮换、总额/日额、RPM、并发、有效期、IP 规则和默认思考强度 |
| 计费 | 输入、输出、缓存创建/读取、图像、按次、服务档位、长上下文、思考强度、用户与分组倍率 |
| 运营 | 用户与全站用量、首字/总耗时、请求 IP 与地区、账号健康、服务器指标、错误中心、告警和审计 |
| 交易 | 余额、充值套餐、兑换码、订单、退款、记账本、推广佣金与微信商家转账 |
| 创作 | 公共模型广场、提示词/规则/Skill 能力库、Fabric.js 无限画布图像工作台 |
| 运维 | SQLite/PostgreSQL、SQLite 一致性备份、加密异地备份、仓库更新器、健康检查和容器发布 |

兼容入口只说明请求格式与响应转换已经实现，不代表任意模型都支持任意协议。实际可用范围由模型目录、分组平台、账号协议和上游能力共同决定。

## 请求链路

```text
SDK / CLI / 第三方客户端
          │
          │  dd- API Key
          ▼
入口协议与密钥鉴权
          │
          ├─ 模型映射与参数归一化
          ├─ 用户 / 密钥限额与并发
          ├─ 分组选择与账号调度
          └─ 账号代理或全局出站代理
          │
          ▼
模型平台 / 第三方兼容上游
          │
          ▼
响应预检与流式转发 → 用量、计费、监控、告警
```

调度器先排除停用、冷却、额度不足、并发已满或模型不匹配的账号，再按分组、优先级和自定义顺序选择候选。可恢复错误先切换账号，再尝试密钥选中的其他同平台分组；请求参数错误不会通过换号掩盖。

完整说明见 [架构文档](docs/ARCHITECTURE.md)。

## 快速启动

### Docker Compose

运行前需要 Docker Engine 和 Compose 插件。

```bash
git clone https://github.com/Lincb522/dengdeng.git
cd dengdeng/deploy
cp .env.example .env
```

为 `JWT_SECRET` 和 `ENCRYPTION_KEY` 分别生成随机值：

```bash
openssl rand -hex 32
openssl rand -hex 32
```

编辑 `deploy/.env`，至少设置：

```dotenv
JWT_SECRET=replace-with-random-value
ENCRYPTION_KEY=replace-with-another-random-value
ADMIN_EMAIL=admin@example.com
ADMIN_PASSWORD=replace-with-a-strong-password
SITE_PUBLIC_URL=https://relay.example.com
```

启动并检查：

```bash
docker compose pull
docker compose up -d
docker compose logs -f dengdeng
curl -fsS http://127.0.0.1:9100/health
```

Compose 默认把服务映射到宿主机 `127.0.0.1:9100`，数据库位于 `deploy/data/dengdeng.db`。公网环境应由 Nginx、Caddy 或同类网关提供 HTTPS，不要直接暴露应用端口。

### 本地开发

版本以仓库和 CI 为准：Go 1.26.5、Node.js 26、pnpm 11.14.0。

```bash
corepack enable
cd frontend
pnpm install --frozen-lockfile
pnpm dev
```

另开终端启动后端：

```bash
cd backend
JWT_SECRET="$(openssl rand -hex 32)" \
ENCRYPTION_KEY="$(openssl rand -hex 32)" \
ADMIN_EMAIL=admin@dengdeng.local \
ADMIN_PASSWORD=local-admin-password \
go run ./cmd/server
```

- Vue 控制台：`http://127.0.0.1:5173`
- Go 服务：`http://127.0.0.1:9100`
- 健康检查：`http://127.0.0.1:9100/health`
- React 图像工作台：`pnpm dev:workbench` 后使用 `http://127.0.0.1:5174/image-workbench/`

开发密码只用于本机。不要在可被外部访问的环境中复用示例值。

### 单二进制构建

Vue 控制台和 React 工作台会构建到 Go 的嵌入目录：

```bash
cd frontend
pnpm install --frozen-lockfile
pnpm build

cd ../backend
go build -trimpath -o dengdeng ./cmd/server
JWT_SECRET="$(openssl rand -hex 32)" ./dengdeng
```

## 首次配置顺序

1. 在“系统设置”确认站点公开地址、注册策略、邮件、安全策略和用户默认额度。
2. 创建分组，选择平台、公开状态、倍率、缓存规则、服务档位和长上下文规则。
3. 添加上游账号并绑定一个或多个同平台分组；需要时为账号选择独立代理。
4. 刷新账号模型与额度，执行账号探测，确认凭证状态和上游响应。
5. 检查模型目录和模型价格。第三方 API Key 账号应以该上游实际模型清单为准。
6. 创建测试用户和 `dd-` 密钥，验证模型列表、非流式、流式、用量与扣费。
7. 只有在回调、签名和人工核验都通过后，才启用支付或推广提现。

系统启动配置与网页运行设置的边界见 [系统设置](docs/SYSTEM_SETTINGS.md)。

## API 接入

Base URL 使用站点根地址或 `/v1` 取决于客户端是否自动追加版本路径。服务对部分常见重复路径提供兼容别名，但新配置应避免生成 `/v1/v1`。

```bash
curl https://relay.example.com/v1/models \
  -H 'Authorization: Bearer dd-your-key'
```

OpenAI Chat Completions：

```bash
curl https://relay.example.com/v1/chat/completions \
  -H 'Authorization: Bearer dd-your-key' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "your-model",
    "messages": [{"role": "user", "content": "你好"}]
  }'
```

Anthropic Messages：

```bash
curl https://relay.example.com/v1/messages \
  -H 'x-api-key: dd-your-key' \
  -H 'anthropic-version: 2023-06-01' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "your-claude-model",
    "max_tokens": 256,
    "messages": [{"role": "user", "content": "你好"}]
  }'
```

全部入口、认证头、用量查询和错误说明见 [API 文档](docs/API.md)。

## 数据与密钥

- `JWT_SECRET` 用于控制台会话签名；`ENCRYPTION_KEY` 用于上游凭证、可恢复用户密钥、支付秘密和 OAuth Secret 等字段加密。生产环境必须分别设置并妥善保存。
- 用户 API 密钥同时保存不可逆摘要用于鉴权，以及可选的加密密文用于跨设备查看和快速配置。密文只对密钥所属用户开放，并受可配置的 step-up 验证控制。
- `ENCRYPTION_KEY` 变更前必须完成数据迁移。直接替换会使历史密文无法解密。
- SQLite 自动备份只清理系统自动生成的快照；管理员手动备份不会被保留策略删除。
- PostgreSQL 可承载业务数据，但内置数据库快照功能当前只支持 SQLite。
- 完整数据库、运行环境文件和全栈备份都属于敏感数据。即使备份脚本移除了支付绑定，也仍可能包含用户数据和已加密的上游凭证。

安全边界和报告方式见 [安全说明](SECURITY.md)，生产运行见 [部署手册](docs/DEPLOYMENT.md)。

## 已知边界

- 网关并发槽、等待队列和部分调度状态保存在进程内；当前生产设计以单网关实例为基准。直接横向复制实例会产生分散的并发计数。
- Agent Identity 当前覆盖 HTTP、SSE 和相关 JSON 辅助接口，不包含 Responses WebSocket v2。
- 第三方兼容上游的模型清单、额度查询、错误格式和媒体接口并不统一；系统只对已识别格式做规范化。
- 支付渠道是否可用取决于商户资质、回调可达性、签名配置和渠道自身状态。订单核验不能替代支付平台对账。
- 公开模型广场和渠道状态只展示公开、启用且当前用户可见的内容；管理员视图可以检查私有分组。

## 仓库结构

```text
backend/                    Go 网关、管理 API、调度、计费与持久化
frontend/src/               Vue 3 用户端与管理端
frontend/workbench/         React 无限画布图像工作台
deploy/                     Compose、Nginx、systemd、备份与更新器
docs/                       API、架构、部署和专项说明
scripts/                    导入检查与端到端验证脚本
.github/workflows/          质量检查与多架构容器发布
```

## 文档

| 文档 | 内容 |
| --- | --- |
| [PRODUCT.md](PRODUCT.md) | 产品边界、角色、领域对象和非目标 |
| [docs/API.md](docs/API.md) | 公共 API、认证、示例、错误和兼容规则 |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | 组件、请求生命周期、调度、计费和扩展边界 |
| [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) | Docker、systemd、HTTPS、备份、更新和恢复 |
| [docs/SYSTEM_SETTINGS.md](docs/SYSTEM_SETTINGS.md) | 启动配置、网页设置、安全策略和运行策略 |
| [docs/PAYMENTS.md](docs/PAYMENTS.md) | 支付渠道、金额换算、订单状态、退款和对账 |
| [docs/AGENT_IDENTITY.md](docs/AGENT_IDENTITY.md) | Codex Agent Identity 的导入、签名和安全边界 |
| [docs/REFERRAL_CASH_PAYOUT.md](docs/REFERRAL_CASH_PAYOUT.md) | 推广佣金、提现状态和微信商家转账 |
| [CONTRIBUTING.md](CONTRIBUTING.md) | 开发、测试与提交要求 |
| [SECURITY.md](SECURITY.md) | 凭据处理、生产加固和漏洞反馈 |
| [CHANGELOG.md](CHANGELOG.md) | 版本变化 |
| [NOTICE.md](NOTICE.md) | 第三方许可证与参考项目 |

## 开源协议

本仓库按 [GNU LGPL-3.0-or-later](LICENSE) 发布。第三方依赖、嵌入源码、品牌名称和图标仍适用各自许可证与商标规则，详见 [NOTICE.md](NOTICE.md) 和 [frontend/workbench/UPSTREAM.md](frontend/workbench/UPSTREAM.md)。
