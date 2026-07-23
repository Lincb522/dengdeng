# Agent Identity

Agent Identity 是 Codex Access Token 登录后生成的长期签名身份，不是普通 ChatGPT OAuth，也不是 API Key。

## 与 CPA、OAuth 的区别

| 凭证 | 来源 | 请求认证 | 是否自动续期 |
| --- | --- | --- | --- |
| CPA Codex OAuth | 浏览器登录或 OAuth 文件 | `Bearer access_token` | 依赖 `refresh_token` |
| Codex PAT | Codex Personal Access Token | `Bearer at-...` | 按 PAT 生命周期 |
| Agent Identity | Codex Access Token 登录生成的 `auth.json` | 每次请求动态生成 `AgentAssertion` | 不依赖 OAuth Token |

CPA 当前没有 Agent Identity 的 Runtime 注册、Task 注册或 Ed25519 请求签名实现。DengDeng 的 Agent Identity 行为以 Sub2API 当前实现为准，CPA 仅用于核对 Codex 请求头和 Responses 协议兼容。

## 凭证结构

```json
{
  "auth_mode": "agentIdentity",
  "agent_identity": {
    "agent_runtime_id": "...",
    "agent_private_key": "...",
    "task_id": "...",
    "account_id": "...",
    "chatgpt_user_id": "...",
    "email": "...",
    "plan_type": "..."
  }
}
```

- `agent_private_key` 是 PKCS#8 编码的 Ed25519 私钥。
- `agent_runtime_id` 标识 Codex 已登记的 Runtime。
- `task_id` 是可轮换的上游任务凭证，可以缺省。
- `account_id` 是 ChatGPT Account / Team 标识，也是导入时的唯一更新键。
- `chatgpt_user_id` 用于保留成员身份，但不参与去重；同一用户可以属于多个 Team。

导入后只保存以上 Agent Identity 字段。文件里同时存在的 Access Token、Refresh Token、ID Token、Web Session 和 OAuth 过期时间都会被丢弃。整个凭证 JSON 使用服务端加密字段落库，管理接口不会返回私钥。

## 请求流程

### 1. 确保 Task 可用

当账号没有 `task_id` 时，服务端签名：

```text
agent_runtime_id + ":" + RFC3339_UTC_TIMESTAMP
```

随后请求：

```http
POST https://auth.openai.com/api/accounts/v1/agent/{agent_runtime_id}/task/register
Content-Type: application/json

{
  "timestamp": "...",
  "signature": "BASE64_ED25519_SIGNATURE"
}
```

上游可能直接返回 `task_id`，也可能返回 `encrypted_task_id`。加密结果使用从 Ed25519 私钥派生的 Curve25519 密钥解封，得到真实 `task_id`，再加密回写数据库。

Task 注册在网关、额度查询和账号探针之间共用账号级锁。锁内会重新读取数据库；如果另一个请求已经写入新 Task，当前请求直接复用，不会重复注册。

### 2. 每次请求动态签名

每次上游请求都重新签名：

```text
agent_runtime_id + ":" + task_id + ":" + RFC3339_UTC_TIMESTAMP
```

签名和身份字段组成 JSON 信封，经 Base64URL 编码后写入请求头：

```http
Authorization: AgentAssertion BASE64URL_JSON
```

Responses、Chat Completions 转换、Claude Code 转 Responses、生图、额度查询和账号认证探针使用同一套签名流程。企业 FedRAMP 身份还会在这些 ChatGPT 后端请求中发送 `x-openai-fedramp: true`。

### 3. 精确恢复失效 Task

只有上游明确返回 Task 失效的 `401`，例如 `invalid_task_id`、`task_not_found` 或 `task_expired`，才会注册新 Task 并重放一次尚未下发内容的请求。

恢复时会携带本次请求观察到的旧 `task_id`。如果锁内发现数据库已经是另一个新 Task，说明其他请求已完成恢复，当前请求直接复用。网络错误、`5xx` 或无法确认结果的注册失败不会盲目重试，避免生成多个并发 Task。

## 获取与导入

1. 在 `~/.codex/config.toml` 中设置：

   ```toml
   cli_auth_credentials_store = "file"
   ```

2. 使用有权限的 Codex Access Token 登录：

   ```bash
   printf '%s' "$CODEX_ACCESS_TOKEN" | codex login --with-access-token
   ```

3. 在 DengDeng 管理端打开“上游账号”，选择 OpenAI 分组和 `Agent Identity`。
4. 导入 `~/.codex/auth.json`。
5. 刷新账号额度或执行一次请求。缺少 `task_id` 时系统会在请求前完成注册。

普通 `codex login` 生成的 OAuth 凭证、CPA OAuth 文件、ChatGPT Web Session 或普通 API Access Token 不能直接作为 Agent Identity 导入。

## 安全边界

- 不在管理端接收或交换 Web Session。
- 不把 OAuth Token 转存为 Agent Identity。
- 私钥和完整 `AgentAssertion` 不进入 API 响应、运行日志或监控错误详情。
- 同一 ChatGPT Account 再次导入时原位轮换 Runtime；不同 Team 独立保存。
- 账号探针会执行签名后的额度请求，不能只靠域名连通性把无效凭证标记为可用。

## 对照来源

- [OpenAI Codex 身份认证](https://learn.chatgpt.com/docs/auth)
- [OpenAI Codex Access Token](https://learn.chatgpt.com/docs/enterprise/access-tokens)
- [Sub2API Agent Identity 实现](https://github.com/Wei-Shaw/sub2api/blob/ba88cc239/backend/internal/service/openai_agent_identity.go)
- [CLIProxyAPI（CPA）](https://github.com/router-for-me/CLIProxyAPI/tree/f71ec0eb)

本次对照基于 2026-07-23 拉取的 Sub2API `ba88cc239` 与 CPA `f71ec0eb`。CPA 用来核对 Codex OAuth/PAT 请求协议；Runtime 注册、Task 生命周期和 AgentAssertion 以 Sub2API 的 Agent Identity 实现为准。
