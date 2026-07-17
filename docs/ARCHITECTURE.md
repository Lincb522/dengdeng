# 架构说明

## 组成

```text
客户端 / SDK / CLI
        │  Anthropic、OpenAI、Gemini 兼容 API
        ▼
Gin 网关（鉴权、模型映射、协议适配、限额）
        │
调度与账号池（分组、优先级、冷却、代理）
        │
上游平台 API

控制台（Vue 3） ────────► 管理与用户 API ────────► SQLite / PostgreSQL
```

## 请求链路

1. 网关从请求头读取 `dd-` API Key，并校验密钥状态、额度、分组和调用限制。
2. 根据兼容端点和模型目录确定目标平台及上游模型名。
3. 调度器从对应分组选择可用账号；账号发生鉴权、限流或上游错误时进入冷却，并尝试下一账号。
4. 请求经可选出站代理转发给上游。流式响应以 SSE 形式透传，同时提取最后的 usage 数据。
5. 服务按模型价格、输入/输出/缓存 Token、用户倍率和分组倍率结算，并记录用量、账单与运营指标。

## 代码边界

| 目录 | 职责 |
| --- | --- |
| `backend/cmd/server` | 服务启动入口 |
| `backend/internal/config` | YAML 与环境变量配置 |
| `backend/internal/model` | GORM 数据模型 |
| `backend/internal/store` | 数据库迁移与初始化 |
| `backend/internal/gateway` | API 鉴权、协议适配、转发、流式与 usage 提取 |
| `backend/internal/service` | 调度、计价、支付、邮件、告警与运行指标 |
| `backend/internal/handler` | 控制台、用户端和管理端 HTTP API |
| `frontend/src` | Vue 控制台、主题、组件与 API 客户端 |
| `deploy` | Docker、systemd 与 Nginx 部署模板 |

## 数据与安全边界

- 平台 API Key 只保存不可逆摘要；明文只在创建时返回一次。
- 上游账户凭据使用 AES-GCM 加密后存储，密钥来自 `ENCRYPTION_KEY`。
- 支付配置、OAuth 凭据、数据库及备份均属于敏感数据，不应出现在浏览器端、日志或版本库。
- 前端是控制台界面，所有鉴权、结算和账户选择均在后端完成。
