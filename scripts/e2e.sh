#!/usr/bin/env bash
# 端到端联调:管理员建组/账号 -> 用户注册/建密钥 -> 三平台转发(流式+非流式)
# -> 计费落账 -> 故障转移 -> 兑换码。
# 前提:主服务 :9100(admin@test.local/admin12345),mock 上游 :9200。
set -euo pipefail

BASE="${BASE:-http://127.0.0.1:9100}"
MOCK="${MOCK:-http://127.0.0.1:9200}"
PASS=0
FAIL=0

say() { printf '\n\033[1;33m== %s ==\033[0m\n' "$*"; }
ok() { PASS=$((PASS + 1)); printf '\033[32mPASS\033[0m %s\n' "$*"; }
bad() { FAIL=$((FAIL + 1)); printf '\033[31mFAIL\033[0m %s\n' "$*"; }
need() { # need <desc> <actual> <expect-substr>
  if [[ "$2" == *"$3"* ]]; then ok "$1"; else bad "$1 | got: ${2:0:300}"; fi
}

jqget() { jq -r "$2 // empty" <<<"$1"; }

# The seeded login agreement requires clients to echo the current revision on
# login/registration. Fetch it once so the flow works whether or not the terms
# gate is enabled.
REV=$(jqget "$(curl -s "$BASE/api/settings")" '.data.login_agreement.revision')

say "管理员登录"
ADMIN_RESP=$(curl -s "$BASE/api/auth/login" -d "{\"email\":\"admin@test.local\",\"password\":\"admin12345\",\"terms_revision\":\"$REV\"}")
ADMIN_TOKEN=$(jqget "$ADMIN_RESP" '.data.token')
[[ -n "$ADMIN_TOKEN" ]] && ok "admin login" || { bad "admin login"; exit 1; }
AH="Authorization: Bearer $ADMIN_TOKEN"

say "创建四个平台分组 + mock 上游账号"
for P in anthropic openai gemini grok; do
  G=$(curl -s "$BASE/api/admin/groups" -H "$AH" -d "{\"name\":\"grp-$P\",\"platform\":\"$P\"}")
  GID=$(jqget "$G" '.data.id')
  A=$(curl -s "$BASE/api/admin/accounts" -H "$AH" -d "{\"group_id\":$GID,\"name\":\"mock-$P\",\"base_url\":\"$MOCK\",\"api_key\":\"mock-key\"}")
  need "group+account [$P]" "$A" '"id"'
  eval "GID_$P=$GID"
done

say "用户注册 + 管理员充值"
USER_RESP=$(curl -s "$BASE/api/auth/register" -d "{\"email\":\"user1@test.local\",\"password\":\"user12345\",\"terms_revision\":\"$REV\"}")
USER_TOKEN=$(jqget "$USER_RESP" '.data.token')
USER_ID=$(jqget "$USER_RESP" '.data.user.id')
[[ -n "$USER_TOKEN" ]] && ok "user register" || bad "user register"
UH="Authorization: Bearer $USER_TOKEN"
curl -s -X PUT "$BASE/api/admin/users/$USER_ID" -H "$AH" -d '{"add_balance_micro":5000000}' >/dev/null
BAL=$(curl -s "$BASE/api/user/me" -H "$UH")
need "balance = 5 USD" "$BAL" '"balance_micro":5000000'

say "用户为四个平台各建一个 API Key"
for P in anthropic openai gemini grok; do
  GID_VAR="GID_$P"
  K=$(curl -s "$BASE/api/user/keys" -H "$UH" -d "{\"name\":\"key-$P\",\"group_id\":${!GID_VAR}}")
  PLAIN=$(jqget "$K" '.data.plain')
  [[ "$PLAIN" == dd-* ]] && ok "create key [$P]" || bad "create key [$P]"
  eval "KEY_$P=$PLAIN"
done

say "Anthropic 非流式 + 流式"
R=$(curl -s "$BASE/v1/messages" -H "x-api-key: $KEY_anthropic" -H 'content-type: application/json' \
  -d '{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}')
need "anthropic non-stream body" "$R" 'mock reply from anthropic'
R=$(curl -s -N "$BASE/v1/messages" -H "Authorization: Bearer $KEY_anthropic" -H 'content-type: application/json' \
  -d '{"model":"claude-sonnet-4-5","stream":true,"messages":[{"role":"user","content":"hi"}]}')
need "anthropic stream SSE" "$R" 'message_delta'

say "OpenAI 非流式 + 流式(自动注入 include_usage)"
R=$(curl -s "$BASE/v1/chat/completions" -H "Authorization: Bearer $KEY_openai" -H 'content-type: application/json' \
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}')
need "openai non-stream body" "$R" 'mock reply from openai'
R=$(curl -s -N "$BASE/v1/chat/completions" -H "Authorization: Bearer $KEY_openai" -H 'content-type: application/json' \
  -d '{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}')
need "openai stream DONE" "$R" '[DONE]'

say "Gemini 非流式 + 流式"
R=$(curl -s "$BASE/v1beta/models/gemini-2.5-pro:generateContent" -H "x-goog-api-key: $KEY_gemini" \
  -H 'content-type: application/json' -d '{"contents":[{"parts":[{"text":"hi"}]}]}')
need "gemini non-stream body" "$R" 'mock reply from gemini'
R=$(curl -s -N "$BASE/v1beta/models/gemini-2.5-pro:streamGenerateContent?alt=sse&key=$KEY_gemini" \
  -H 'content-type: application/json' -d '{"contents":[{"parts":[{"text":"hi"}]}]}')
need "gemini stream body" "$R" 'gemini stream'

say "Grok:Chat 非流式 + 流式 + Responses + Claude Messages 反代桥接"
R=$(curl -s "$BASE/v1/chat/completions" -H "Authorization: Bearer $KEY_grok" -H 'content-type: application/json' \
  -d '{"model":"grok-4.5","messages":[{"role":"user","content":"hi"}]}')
need "grok chat non-stream body" "$R" 'mock reply'
R=$(curl -s -N "$BASE/v1/chat/completions" -H "Authorization: Bearer $KEY_grok" -H 'content-type: application/json' \
  -d '{"model":"grok-4.5","stream":true,"messages":[{"role":"user","content":"hi"}]}')
need "grok chat stream DONE" "$R" '[DONE]'
R=$(curl -s "$BASE/v1/responses" -H "Authorization: Bearer $KEY_grok" -H 'content-type: application/json' \
  -d '{"model":"grok-4.5","input":"hi"}')
need "grok responses body" "$R" 'mock reply from grok'
R=$(curl -s "$BASE/v1/messages" -H "x-api-key: $KEY_grok" -H 'content-type: application/json' \
  -d '{"model":"grok-4.5","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}')
need "grok via claude messages bridge" "$R" 'mock reply from grok'

say "计费校验(计费调用后余额应减少)"
sleep 1
ME=$(curl -s "$BASE/api/user/me" -H "$UH")
BAL_NOW=$(jqget "$ME" '.data.balance_micro')
if [[ "$BAL_NOW" -lt 5000000 ]]; then ok "balance deducted ($BAL_NOW < 5000000)"; else bad "balance not deducted ($BAL_NOW)"; fi

USAGE=$(curl -s "$BASE/api/user/usage?size=20" -H "$UH")
CNT=$(jqget "$USAGE" '.data.total')
if [[ "$CNT" -ge 6 ]]; then ok "usage logs written ($CNT)"; else bad "usage logs missing ($CNT)"; fi
need "usage has token detail" "$USAGE" '"input_tokens":120'

say "思考强度:官方档位 + 按档位计费倍率"
# 管理员把 high 档倍率调成 2x,其余保持 1x。
POL=$(curl -s "$BASE/api/admin/runtime-settings" -H "$AH")
POL_PUT=$(jq '.data | .reasoning_effort_multipliers.high = 2' <<<"$POL")
R=$(curl -s -X PUT "$BASE/api/admin/runtime-settings" -H "$AH" -d "$POL_PUT")
need "set high effort multiplier = 2" "$R" '"high":2'
# 同一模型分别用 medium / high 发起,mock 上游返回固定 token 用量,
# 因此 high 的费用应恰好是 medium 的 2 倍。
curl -s "$BASE/v1/chat/completions" -H "Authorization: Bearer $KEY_openai" -H 'content-type: application/json' \
  -d '{"model":"gpt-4o","reasoning_effort":"medium","messages":[{"role":"user","content":"hi"}]}' >/dev/null
curl -s "$BASE/v1/chat/completions" -H "Authorization: Bearer $KEY_openai" -H 'content-type: application/json' \
  -d '{"model":"gpt-4o","reasoning_effort":"high","messages":[{"role":"user","content":"hi"}]}' >/dev/null
sleep 1
USAGE=$(curl -s "$BASE/api/user/usage?size=10" -H "$UH")
COST_MED=$(jq -r '[.data.items[] | select(.reasoning_effort=="medium")][0].cost_micro' <<<"$USAGE")
COST_HIGH=$(jq -r '[.data.items[] | select(.reasoning_effort=="high")][0].cost_micro' <<<"$USAGE")
if [[ -n "$COST_MED" && "$COST_MED" != "null" && "$COST_HIGH" == "$((COST_MED * 2))" ]]; then
  ok "high effort billed at 2x ($COST_HIGH = 2 * $COST_MED)"
else
  bad "effort billing mismatch (medium=$COST_MED high=$COST_HIGH)"
fi
# 旧档位 fast/max 已并入官方档位;fast 建密钥会被存成 low。
K=$(curl -s "$BASE/api/user/keys" -H "$UH" -d "{\"name\":\"key-effort\",\"group_id\":$GID_openai,\"reasoning_effort\":\"fast\"}")
need "legacy fast stored as official low" "$K" '"reasoning_effort":"low"'
R=$(curl -s "$BASE/api/user/keys" -H "$UH" -d "{\"name\":\"key-bad\",\"group_id\":$GID_openai,\"reasoning_effort\":\"turbo\"}")
need "invalid effort rejected" "$R" 'invalid reasoning effort'
# 恢复倍率,避免影响后续用例。
curl -s -X PUT "$BASE/api/admin/runtime-settings" -H "$AH" -d "$(jq '.data' <<<"$POL")" >/dev/null

say "故障转移:坏账号(高优先级)+ 好账号,请求仍成功"
GID_VAR="GID_anthropic"
curl -s "$BASE/api/admin/accounts" -H "$AH" \
  -d "{\"group_id\":${!GID_VAR},\"name\":\"broken\",\"base_url\":\"http://127.0.0.1:9999\",\"api_key\":\"x\",\"priority\":99}" >/dev/null
R=$(curl -s "$BASE/v1/messages" -H "x-api-key: $KEY_anthropic" -H 'content-type: application/json' \
  -d '{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}')
need "failover to healthy account" "$R" 'mock reply from anthropic'
ACCTS=$(curl -s "$BASE/api/admin/accounts?group_id=${!GID_VAR}" -H "$AH")
need "broken account got cooldown" "$ACCTS" '"cooldown_until"'

say "兑换码:管理员生成 -> 用户兑换"
GEN=$(curl -s "$BASE/api/admin/redeem-codes" -H "$AH" -d '{"count":1,"amount_micro":2000000}')
CODE=$(jqget "$GEN" '.data.codes[0]')
RED=$(curl -s "$BASE/api/user/redeem" -H "$UH" -d "{\"code\":\"$CODE\"}")
need "redeem ok" "$RED" '"amount_micro":2000000'
RED2=$(curl -s "$BASE/api/user/redeem" -H "$UH" -d "{\"code\":\"$CODE\"}")
need "redeem twice rejected" "$RED2" 'already used'

say "权限与边界"
R=$(curl -s "$BASE/api/admin/users" -H "$UH")
need "non-admin blocked from admin api" "$R" 'admin only'
R=$(curl -s "$BASE/v1/messages" -H "x-api-key: dd-invalid" -d '{"model":"m"}')
need "invalid api key rejected" "$R" 'invalid API key'
# Claude 和 Gemini 分组从 OpenAI Chat 入口进来都会走协议桥接。
R=$(curl -s "$BASE/v1/chat/completions" -H "Authorization: Bearer $KEY_gemini" -H 'content-type: application/json' \
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}')
need "gemini group via openai chat bridge" "$R" 'mock reply from gemini'
R=$(curl -s "$BASE/v1/chat/completions" -H "Authorization: Bearer $KEY_anthropic" -H 'content-type: application/json' \
  -d '{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}')
need "claude group via openai chat bridge" "$R" 'mock reply from anthropic'

say "安全:上游 token 在数据库中已加密"
DBFILE="${DENGDENG_DB:-/tmp/dengdeng-e2e/test.db}"
if command -v sqlite3 >/dev/null 2>&1 && [[ -f "$DBFILE" ]]; then
  RAW=$(sqlite3 "$DBFILE" "SELECT api_key FROM upstream_accounts LIMIT 1;")
  need "upstream key stored as ciphertext" "$RAW" 'enc:v1:'
  if [[ "$RAW" == *"mock-key"* ]]; then bad "plaintext key found in DB!"; else ok "no plaintext key in DB"; fi
else
  printf 'SKIP  sqlite3 not available or db missing\n'
fi

say "安全:响应头"
HDR=$(curl -s -D - -o /dev/null "$BASE/login")
need "X-Frame-Options present" "$HDR" 'X-Frame-Options: DENY'
need "X-Content-Type-Options present" "$HDR" 'nosniff'
need "CSP present on console" "$HDR" 'Content-Security-Policy'

say "安全:JWT 失效(改密码后旧 token 作废)"
OLD_UH="$UH"
CP=$(curl -s "$BASE/api/user/password" -H "$OLD_UH" -d '{"old_password":"user12345","new_password":"user54321new"}')
need "change password returns new token" "$CP" '"token"'
NEW_TOKEN=$(jqget "$CP" '.data.token')
R=$(curl -s "$BASE/api/user/me" -H "$OLD_UH")
need "old token rejected after password change" "$R" 'session expired'
R=$(curl -s "$BASE/api/user/me" -H "Authorization: Bearer $NEW_TOKEN")
need "new token works" "$R" '"email":"user1@test.local"'

say "安全:封禁用户后其 token 立即失效"
curl -s -X PUT "$BASE/api/admin/users/$USER_ID" -H "$AH" -d '{"status":"disabled"}' >/dev/null
R=$(curl -s "$BASE/api/user/me" -H "Authorization: Bearer $NEW_TOKEN")
if [[ "$R" == *"session expired"* || "$R" == *"disabled"* ]]; then ok "banned user token invalidated"; else bad "banned user still authorized | $R"; fi
curl -s -X PUT "$BASE/api/admin/users/$USER_ID" -H "$AH" -d '{"status":"active"}' >/dev/null

say "安全:登录失败锁定(连续错误密码触发锁定)"
for i in 1 2 3 4 5 6; do
  curl -s "$BASE/api/auth/login" -d '{"email":"lockme@test.local","password":"wrongpass1"}' >/dev/null
done
R=$(curl -s "$BASE/api/auth/login" -d '{"email":"lockme@test.local","password":"wrongpass1"}')
need "account lockout after repeated failures" "$R" 'too many failed attempts'

printf '\n\033[1m===== e2e result: %d passed, %d failed =====\033[0m\n' "$PASS" "$FAIL"
[[ "$FAIL" -eq 0 ]]
