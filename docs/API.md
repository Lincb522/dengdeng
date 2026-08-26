# 公共 API

本文只描述使用 `dd-` 密钥调用的公共网关。控制台 `/api/user/*` 与 `/api/admin/*` 使用登录 JWT，属于前端内部管理接口，不承诺与公共模型 API 相同的兼容策略。

## Base URL 与认证

示例站点：

```text
https://relay.example.com
```

OpenAI 兼容客户端通常把 Base URL 配为 `https://relay.example.com/v1`；Anthropic 客户端对 Base URL 的拼接方式不完全一致，可使用站点根地址或 `/v1`。配置后先查看客户端最终请求路径，避免重复拼接版本号。

网关从以下位置读取密钥：

```http
Authorization: Bearer dd-your-key
```

```http
x-api-key: dd-your-key
```

Gemini 兼容调用也支持 `x-goog-api-key` 和查询参数 `key`。密钥必须属于启用用户，处于有效期内，并通过 IP、额度、RPM、并发与分组检查。

## 入口一览

| 协议 | 方法 | 路径 | 说明 |
| --- | --- | --- | --- |
| 通用 | `GET` | `/v1/models` | 返回这把密钥实际可用的模型 |
| 通用 | `GET` | `/v1/usage` | 返回余额、总额度、日额度和按次额度 |
| 通用 | `GET` | `/v1/creation-library` | 返回当前用户可用的提示词、规则和 Skill |
| OpenAI | `POST` | `/v1/chat/completions` | Chat Completions，支持流式 |
| OpenAI | `POST` | `/v1/responses` | Responses，支持流式 |
| OpenAI | `POST` | `/v1/responses/compact` | Responses 上下文压缩 |
| OpenAI | `POST` | `/v1/responses/input_tokens` | 输入 Token 估算 |
| Codex | `GET` | `/backend-api/codex/models` | Codex 账号实时模型清单 |
| Anthropic | `POST` | `/v1/messages` | Messages，支持流式 |
| Anthropic | `POST` | `/v1/messages/count_tokens` | Messages Token 计数 |
| Gemini | `POST` | `/v1beta/models/:model:generateContent` | 原生内容生成 |
| Gemini | `POST` | `/v1beta/models/:model:streamGenerateContent` | 原生流式生成 |
| 图像 | `POST` | `/v1/images/generations` | 同步生成 |
| 图像 | `POST` | `/v1/images/edits` | 图像编辑，支持 multipart |
| 图像 | `POST` | `/v1/images/generations/async` | 创建异步任务 |
| 图像 | `GET` | `/v1/images/tasks/:task_id` | 查询当前密钥创建的任务 |
| xAI | `POST` | `/v1/videos/generations` | 视频生成 |
| xAI | `GET` | `/v1/videos/:media_id` | 查询视频任务或媒体 |
| xAI | `POST` | `/v1/audio/speech` | 语音生成 |
| xAI | `POST` | `/v1/audio/transcriptions` | 音频转写 |
| xAI | `POST` | `/v1/search` | 搜索入口 |

OpenAI、Anthropic 和图像入口另有不带 `/v1` 的兼容别名。Anthropic 还保留 `/v1/v1/messages` 与 `/v1/v1/messages/count_tokens`，用于兼容会重复追加 `/v1` 的旧客户端。新接入不应依赖重复路径。

## 模型列表

```bash
curl -sS https://relay.example.com/v1/models \
  -H 'Authorization: Bearer dd-your-key'
```

返回 OpenAI 风格列表：

```json
{
  "object": "list",
  "data": [
    {"id": "model-id", "object": "model", "owned_by": "openai"}
  ]
}
```

模型来源按账号类型区分：

- API Key 分组会向当前可用的真实上游查询模型，并合并成功结果。
- OAuth 或其他无法稳定执行模型发现的凭证使用本地模型目录。
- `?platform=openai` 只筛选密钥已经绑定且可用的平台；未绑定平台返回 `403`。
- API Key 上游全部查询失败时返回 `502`，不会用本站模型目录伪装上游结果。

## OpenAI Chat Completions

```bash
curl -sS https://relay.example.com/v1/chat/completions \
  -H 'Authorization: Bearer dd-your-key' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "model-id",
    "messages": [{"role": "user", "content": "你好"}],
    "stream": false
  }'
```

设置 `stream: true` 后响应使用 SSE，并以兼容的结束事件关闭。Claude、Gemini、Grok 和国产兼容账号能否承接此入口，取决于账号协议与模型配置；网关只对已经实现的组合执行转换。

## OpenAI Responses

```bash
curl -sS https://relay.example.com/v1/responses \
  -H 'Authorization: Bearer dd-your-key' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "model-id",
    "input": "你好"
  }'
```

Codex CLI 的 Provider 示例：

```toml
model_provider = "dengdeng"
model = "model-id"
cli_auth_credentials_store = "file"

[model_providers.dengdeng]
name = "DengDeng AI"
base_url = "https://relay.example.com/v1"
wire_api = "responses"
requires_openai_auth = true
```

密钥放在 Codex 读取的认证存储中，不要写入仓库。可在用户端“API 密钥 → 快速配置”生成与当前密钥一致的配置。

## Anthropic Messages

```bash
curl -sS https://relay.example.com/v1/messages \
  -H 'x-api-key: dd-your-key' \
  -H 'anthropic-version: 2023-06-01' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "model-id",
    "max_tokens": 512,
    "messages": [{"role": "user", "content": "你好"}]
  }'
```

Claude Code 常用环境变量：

```bash
export ANTHROPIC_BASE_URL="https://relay.example.com"
export ANTHROPIC_AUTH_TOKEN="dd-your-key"
export CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
export ANTHROPIC_MODEL="model-id"
claude
```

这里的模型可以是 Anthropic 模型，也可以是由分组配置允许从 Messages 转换到其他协议的模型。工具调用是否可用仍以对应转换器和上游为准。

## Gemini

```bash
curl -sS 'https://relay.example.com/v1beta/models/model-id:generateContent' \
  -H 'x-goog-api-key: dd-your-key' \
  -H 'Content-Type: application/json' \
  -d '{
    "contents": [{"parts": [{"text": "你好"}]}]
  }'
```

流式路径使用 `:streamGenerateContent`。需要 SSE 时，客户端可按 Gemini 约定附加 `alt=sse`。

## 图像

同步生成：

```bash
curl -sS https://relay.example.com/v1/images/generations \
  -H 'Authorization: Bearer dd-your-key' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "image-model-id",
    "prompt": "a paper boat on a quiet lake",
    "size": "1024x1024"
  }'
```

异步生成先调用 `/v1/images/generations/async`。服务返回 `202` 和 `task_id` 后，使用同一把密钥查询 `/v1/images/tasks/:task_id`。异步任务依赖管理端已配置图像存储服务。

图像编辑可使用 JSON 或 multipart。Nginx 示例把请求体限制设为 `65m`；应用网关限制为 64 MiB。外层 CDN、WAF 或反向代理也必须允许所需大小。

## 用量查询

```bash
curl -sS https://relay.example.com/v1/usage \
  -H 'Authorization: Bearer dd-your-key'
```

响应不使用控制台的 `data` 包装，便于 CCSwitch 等工具直接提取：

```json
{
  "is_active": true,
  "remaining": 12.5,
  "balance": 12.5,
  "unit": "USD",
  "plan_name": "余额",
  "remaining_requests": 0,
  "quota": {"limit": 20, "used": 3.2, "remaining": 16.8},
  "daily_quota": {"limit": 2, "used": 0.4, "remaining": 1.6}
}
```

金额单位为 USD。`quota.limit` 或 `daily_quota.limit` 为 `0` 表示该项未设置上限，此时对应 `remaining` 也返回 `0`，不能把它解释为额度耗尽。

## 路由与计费

请求必须同时满足：

1. 密钥已选分组能够承接当前入口和模型；
2. 用户、密钥与平台额度未耗尽；
3. RPM、并发、有效期与 IP 规则允许；
4. 至少一个账号状态正常、未冷却、未超过额度且有并发槽；
5. 上游响应格式能被当前协议适配器处理。

网关会记录输入、输出、缓存创建、缓存读取、图像、首字耗时、总耗时、排队耗时、路由账号和最终费用。预授权用于避免并发请求重复消费同一余额；请求结束后按实际用量结算并释放预占。

`reasoning_effort`、Anthropic `speed` 和 OpenAI `service_tier` 可能触发独立倍率。计费依据以用量明细中保存的模型价格、分组倍率、用户倍率、服务档位和长上下文规则为准。

## 错误

公共网关尽量返回当前入口兼容的错误对象，并保留有效 HTTP 状态。常见状态：

| 状态 | 含义 | 检查项 |
| --- | --- | --- |
| `400` | 请求或模型参数无法处理 | JSON、模型名、端点、工具参数、分组协议 |
| `401` | 密钥无效或上游凭证拒绝 | `dd-` 密钥、Authorization 头、账号凭证状态 |
| `402` | 余额、按次或平台/密钥额度不足 | 用户余额、有效期套餐、总额、日额、平台额度 |
| `403` | 当前请求不允许 | IP 规则、平台可见性、风控、上游权限 |
| `404` | 路径或异步任务不存在 | 最终 URL、任务所属用户与密钥 |
| `413` | 请求体过大 | CDN、Nginx、WAF 和应用限制 |
| `429` | RPM、并发或上游限流 | 用户/密钥/账号并发、队列、上游冷却 |
| `502` | 上游响应不可读取或格式错误 | 上游原始状态、SSE/JSON、代理、模型协议 |
| `503` | 没有可调度上游 | 分组、账号状态、模型限制、额度、冷却和探测结果 |

管理端“运行监控”和“错误中心”分别保存 API 调用错误与站点操作错误。排障时用响应中的请求 ID 关联用量明细、账号探测和服务日志，不要在工单中附上完整密钥或请求正文。

## 兼容边界

- `/v1/models` 对 API Key 账号优先返回真实上游模型，不保证与本站模型目录完全一致。
- Responses WebSocket v2 尚未开放；HTTP 和 SSE 不应被描述为 WebSocket 支持。
- 未注册的 `/v1/*`、`/v1beta/*` 和 `/api/*` 路径返回 JSON `404`，不会回退到前端 HTML。
- 公共 API 请求体上限为 64 MiB；控制台 `/api/*` 请求体上限为 1 MiB。
- 上游可能在 HTTP 200 中返回错误对象，或在 SSE 首个有效事件前失败。网关会对已识别的错误执行分类和故障转移，但不会猜测未知格式。
