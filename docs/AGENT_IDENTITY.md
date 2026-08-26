# Codex Agent Identity

Agent Identity 是 OpenAI Codex 使用的 Ed25519 长期签名身份。它不是普通 API Key，也不是通过 Access Token 和 Refresh Token 刷新的 ChatGPT OAuth 账号。

实现入口：

- [签名与 Task 生命周期](../backend/internal/service/openai_agent_identity.go)
- [导入解析](../backend/internal/importer/importer.go)
- [导入处理](../backend/internal/handler/admin.go)

## 可导入结构

管理端接受包含以下字段的 `auth.json` 或兼容导出：

```json
{
  "auth_mode": "agentIdentity",
  "agent_identity": {
    "agent_runtime_id": "runtime-id",
    "agent_private_key": "base64-pkcs8-ed25519-private-key",
    "task_id": "optional-task-id",
    "account_id": "chatgpt-account-id",
    "chatgpt_user_id": "chatgpt-user-id",
    "email": "optional@example.com",
    "plan_type": "optional"
  }
}
```

必需字段：

- `agent_runtime_id`；
- `agent_private_key`，Base64 编码的 PKCS#8 Ed25519 私钥；
- `account_id` 或兼容的 `chatgpt_account_id`；
- `chatgpt_user_id`。

`task_id` 可以缺省，服务会在第一次需要时注册。FedRAMP 身份还可以包含 `chatgpt_account_is_fedramp`。

导入器也能从 Sub2API 风格 `accounts[].credentials`、单对象、数组或 JSONL 中识别 Agent Identity。选择的目标分组必须是 OpenAI 平台；平台冲突会跳过并返回具体原因。

## 落库内容

导入后只保留 Agent Identity 的长期字段：

```text
agent_runtime_id
agent_private_key
task_id
account_id
chatgpt_user_id
email
plan_type
chatgpt_account_is_fedramp
```

同一文件中并存的 Access Token、Refresh Token、ID Token、API Key、Web Session 和 OAuth 过期时间不会作为这个账号的凭证保存。Agent Identity 不继承 bootstrap OAuth Token 的到期时间。

账号 `Extra` 整体使用服务端字段加密落库。管理接口不返回私钥；运行日志、错误中心和下游错误会清理私钥、Task、Token 与完整 `AgentAssertion`。

同一 ChatGPT Account 再次导入时原位更新 Runtime，而不是创建重复账号。`chatgpt_user_id` 不能作为唯一键，因为同一用户可以属于多个 Account 或 Team。

## Task 注册

没有 `task_id` 时，服务使用私钥签名：

```text
agent_runtime_id + ":" + RFC3339_UTC_TIMESTAMP
```

随后调用：

```http
POST https://auth.openai.com/api/accounts/v1/agent/{agent_runtime_id}/task/register
Content-Type: application/json
```

```json
{
  "timestamp": "RFC3339 UTC",
  "signature": "base64-ed25519-signature"
}
```

上游可以返回明文 `task_id`，也可以返回 `encrypted_task_id`。加密 Task 使用从 Ed25519 Seed 派生的 Curve25519 私钥解封。得到的 Task 会重新加密写入账号记录。

网关请求、额度刷新和管理员账号探测共用账号级锁。锁内会重新读取数据库；另一个请求已经写入新 Task 时，当前请求直接复用，避免并发注册多个 Task。

## 每次请求签名

有可用 Task 后，每个上游请求重新签名：

```text
agent_runtime_id + ":" + task_id + ":" + RFC3339_UTC_TIMESTAMP
```

请求头：

```http
Authorization: AgentAssertion BASE64URL_JSON
```

信封包含 Runtime ID、Task ID、时间戳和 Base64 Ed25519 签名。签名不会持久化或复用。

当前签名路径覆盖：

- Responses 与由 Chat Completions、Anthropic Messages 转换的 Codex 请求；
- `/v1/responses/compact`；
- `/v1/responses/input_tokens` 与 Messages Token 计数；
- `/backend-api/codex/models`；
- 图像、额度刷新和账号认证探测。

FedRAMP 账号会在对应 ChatGPT 后端请求中附加 `x-openai-fedramp: true`。

## Task 失效恢复

只有上游返回 `401` 且错误明确包含 Task 失效标记时才重新注册，例如：

```text
invalid_task_id
task_not_found
task_expired
```

恢复时携带本次请求观察到的旧 Task。锁内发现数据库已是另一个新 Task 时直接复用；否则注册、持久化，并且只重放尚未向客户端发送有效内容的请求。

网络错误、普通 `5xx` 或结果不确定的注册失败不会被解释成 Task 失效。这样可以避免并发生成多个 Task 或在已经输出内容后拼接第二个上游响应。

## 管理端操作

1. 创建或选择 OpenAI 分组。
2. 在“上游账号”选择导入，目标凭证类型为 Agent Identity。
3. 上传本机生成的 `auth.json`，不要把文件粘贴到聊天、Issue 或日志。
4. 检查导入结果中的新增、更新与跳过原因。
5. 执行额度刷新或账号探测；缺少 Task 时会在这里注册。
6. 用绑定该分组的测试密钥调用 `/backend-api/codex/models` 和一次短 Responses 请求。

普通 Codex OAuth 文件只有 Access/Refresh Token 时会作为 OAuth 导入，不能被自动推导成 Agent Identity。普通 OpenAI API Key 也不能转换为签名身份。

## 传输边界

当前支持 HTTP、SSE 和相关 unary JSON 路径。Responses WebSocket v2 尚未启用。WebSocket 需要独立处理连接限流、首包选模、账号黏性、逐帧用量、断线恢复和 Task 轮换，不能用简单 Upgrade 透传替代。

Agent Identity 的可用性最终由上游授权、Runtime、Task 和 Account 状态决定。导入成功只证明结构和私钥有效，账号探测成功也不保证任意模型与请求长期可用。
