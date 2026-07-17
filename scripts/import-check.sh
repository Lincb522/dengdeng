#!/usr/bin/env bash
# 验证:上游账号 OAuth 支持 + sub2api / cpa 两种 JSON 导入 + 加密存储 + OAuth 转发。
# 前提:主服务 :9100(admin@dengdeng.local/admin12345),mock 上游 :9200。
set -euo pipefail

BASE="${BASE:-http://127.0.0.1:9100}"
MOCK="${MOCK:-http://127.0.0.1:9200}"
DBFILE="${DBFILE:-$(cd "$(dirname "$0")/.." && pwd)/backend/data/dengdeng.db}"
PASS=0; FAIL=0
ok() { PASS=$((PASS+1)); printf '\033[32mPASS\033[0m %s\n' "$*"; }
bad() { FAIL=$((FAIL+1)); printf '\033[31mFAIL\033[0m %s\n' "$*"; }
need() { if [[ "$2" == *"$3"* ]]; then ok "$1"; else bad "$1 | got: ${2:0:200}"; fi; }
jqr() { jq -r "$2" <<<"$1"; }

echo "== 管理员登录 =="
AT=$(jqr "$(curl -s "$BASE/api/auth/login" -d '{"email":"admin@dengdeng.local","password":"admin12345"}')" '.data.token')
[[ -n "$AT" && "$AT" != null ]] && ok "admin login" || { bad "admin login"; exit 1; }
AH="Authorization: Bearer $AT"

echo "== 新建隔离分组(openai)=="
GN="imp-$(date +%s)-$RANDOM"
GRESP=$(curl -s "$BASE/api/admin/groups" -H "$AH" -d "{\"name\":\"$GN\",\"platform\":\"openai\",\"rate_multiplier\":1.0}")
GID=$(jqr "$GRESP" '.data.id')
if [[ -z "$GID" || "$GID" == null ]]; then bad "group create | resp: $GRESP"; exit 1; fi
ok "group created (#$GID)"

echo "== 导入 sub2api 格式(1 个 api_key + 1 个 oauth,oauth 高优先级指向 mock)=="
SUB=$(cat <<JSON
{"exported_at":"2026-01-01T00:00:00Z","proxies":[],"accounts":[
 {"name":"s2a-apikey","platform":"openai","type":"api_key","base_url":"$MOCK","priority":10,"credentials":{"api_key":"sk-mock-plain-123"}},
 {"name":"s2a-oauth","platform":"openai","type":"oauth","base_url":"$MOCK","priority":99,"credentials":{"access_token":"oauth-access-abc","refresh_token":"oauth-refresh-def","email":"s2a@example.com","chatgpt_account_id":"acct-s2a","expires_at":"2999-01-01T00:00:00Z"}},
 {"name":"s2a-wrongplatform","platform":"anthropic","type":"api_key","credentials":{"api_key":"x"}}
]}
JSON
)
R=$(curl -s "$BASE/api/admin/accounts/import" -H "$AH" \
  -d "$(jq -n --arg g "$GID" --arg d "$SUB" '{group_id:($g|tonumber),format:"sub2api",data:$d,skip_expired:true}')")
need "sub2api imported 2" "$R" '"imported":2'
need "sub2api skipped wrong platform" "$R" '"skipped":1'

echo "== 导入 cpa 格式(单个 codex oauth,默认 base_url=mock)=="
CPA='{"type":"codex","email":"cpa@example.com","account_id":"acct-cpa","plan_type":"plus","id_token":"eyJhbGciOiJub25lIn0.e30.","access_token":"cpa-access-xyz","session_token":"sess-xyz","refresh_token":"cpa-refresh-xyz","expired":"2999-01-01T00:00:00Z"}'
R=$(curl -s "$BASE/api/admin/accounts/import" -H "$AH" \
  -d "$(jq -n --arg g "$GID" --arg d "$CPA" --arg b "$MOCK" '{group_id:($g|tonumber),format:"cpa",data:$d,base_url:$b,skip_expired:true}')")
need "cpa imported 1" "$R" '"imported":1'

echo "== 校验账号列表(auth_type / 元数据)=="
ACCTS=$(curl -s "$BASE/api/admin/accounts?group_id=$GID" -H "$AH")
CNT=$(jqr "$ACCTS" '.data | length')
[[ "$CNT" == "3" ]] && ok "account count = 3" || bad "account count = $CNT"
OAUTH_CNT=$(jqr "$ACCTS" '[.data[]|select(.auth_type=="oauth")]|length')
[[ "$OAUTH_CNT" == "2" ]] && ok "oauth accounts = 2" || bad "oauth accounts = $OAUTH_CNT"
need "cpa email carried" "$ACCTS" 'cpa@example.com'
need "cpa account_id carried" "$ACCTS" 'acct-cpa'
need "oauth expiry stored" "$ACCTS" '2999-01-01'

echo "== 校验密文落库(access_token / api_key 加密)=="
if command -v sqlite3 >/dev/null 2>&1; then
  ROWS=$(sqlite3 "$DBFILE" "SELECT auth_type||'|'||substr(access_token,1,7)||'|'||substr(api_key,1,7) FROM upstream_accounts WHERE group_id=$GID;")
  need "access_token ciphertext" "$ROWS" 'oauth|enc:v1:'
  if [[ "$ROWS" == *"oauth-access-abc"* || "$ROWS" == *"cpa-access-xyz"* ]]; then bad "plaintext token in DB!"; else ok "no plaintext oauth token in DB"; fi
  RAWKEY=$(sqlite3 "$DBFILE" "SELECT api_key FROM upstream_accounts WHERE group_id=$GID AND auth_type='api_key';")
  need "api_key ciphertext" "$RAWKEY" 'enc:v1:'
  [[ "$RAWKEY" == *"sk-mock-plain-123"* ]] && bad "plaintext api_key in DB!" || ok "no plaintext api_key in DB"
else
  printf 'SKIP  sqlite3 不可用\n'
fi

echo "== OAuth 转发到上游(scheduler 选中高优先级 oauth 账号)=="
UT=$(jqr "$(curl -s "$BASE/api/auth/login" -d '{"email":"demo@dengdeng.local","password":"demo12345"}')" '.data.token')
if [[ -z "$UT" || "$UT" == null ]]; then
  UT=$(jqr "$(curl -s "$BASE/api/auth/register" -d '{"email":"demo@dengdeng.local","password":"demo12345"}')" '.data.token')
fi
UH="Authorization: Bearer $UT"
DEMO_UID=$(jqr "$(curl -s "$BASE/api/user/me" -H "$UH")" '.data.id')
curl -s -X PUT "$BASE/api/admin/users/$DEMO_UID" -H "$AH" -d '{"add_balance_micro":5000000}' >/dev/null
KEY=$(jqr "$(curl -s "$BASE/api/user/keys" -H "$UH" -d "{\"name\":\"imp-key\",\"group_id\":$GID}")" '.data.plain')
[[ "$KEY" == dd-* ]] && ok "user key for group" || bad "user key create"
R=$(curl -s "$BASE/v1/chat/completions" -H "Authorization: Bearer $KEY" -H 'content-type: application/json' \
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}')
need "relay via oauth account succeeds" "$R" 'mock reply from openai'

printf '\n\033[1m===== import-check: %d passed, %d failed =====\033[0m\n' "$PASS" "$FAIL"
[[ "$FAIL" -eq 0 ]]
