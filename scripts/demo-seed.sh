#!/usr/bin/env bash
# 本地演示数据:三平台分组 + mock 上游账号 + demo 用户 + 密钥 + 一批转发流量。
# 前提:主服务 :9100(admin@dengdeng.local/admin12345),mock 上游 :9200(go run ./tools/mockupstream)。
# 生成的用户密钥写入 backend/data/demo-keys.txt(不在终端打印)。
set -euo pipefail

BASE="${BASE:-http://127.0.0.1:9100}"
MOCK="${MOCK:-http://127.0.0.1:9200}"
ADMIN_EMAIL="${ADMIN_EMAIL:-admin@dengdeng.local}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-admin12345}"
OUT="$(cd "$(dirname "$0")/.." && pwd)/backend/data/demo-keys.txt"

jqget() { jq -r "$2" <<<"$1"; }

# 登录协议开启时，登录/注册必须回传当前条款 revision。
REV=$(curl -s "$BASE/api/settings" | jq -r '.data.login_agreement.revision // ""')

echo "==> 管理员登录"
ADMIN_RESP=$(curl -s "$BASE/api/auth/login" -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASSWORD\",\"terms_revision\":\"$REV\"}")
ADMIN_TOKEN=$(jqget "$ADMIN_RESP" '.data.token')
[[ -n "$ADMIN_TOKEN" && "$ADMIN_TOKEN" != "null" ]] || { echo "管理员登录失败: $ADMIN_RESP"; exit 1; }
AH="Authorization: Bearer $ADMIN_TOKEN"

echo "==> 创建四平台分组 + mock 上游账号"
for P in anthropic openai gemini grok; do
  G=$(curl -s "$BASE/api/admin/groups" -H "$AH" -d "{\"name\":\"grp-$P\",\"platform\":\"$P\",\"rate_multiplier\":1.0}")
  G_ID=$(jqget "$G" '.data.id')
  if [[ -z "$G_ID" || "$G_ID" == "null" ]]; then
    # 分组可能已存在(重复执行),从列表里找回 id。
    G_ID=$(curl -s "$BASE/api/admin/groups" -H "$AH" | jq -r ".data[] | select(.name==\"grp-$P\") | .id")
  fi
  [[ -n "$G_ID" && "$G_ID" != "null" ]] || { echo "创建分组失败[$P]: $G"; exit 1; }
  curl -s "$BASE/api/admin/accounts" -H "$AH" \
    -d "{\"group_id\":$G_ID,\"name\":\"demo-$P-1\",\"base_url\":\"$MOCK\",\"api_key\":\"demo-upstream-key-$P\"}" >/dev/null
  eval "GID_$P=$G_ID"
  echo "    $P -> group #$G_ID"
done

echo "==> 注册 demo 用户并充值 20 USD"
USER_RESP=$(curl -s "$BASE/api/auth/register" -d "{\"email\":\"demo@dengdeng.local\",\"password\":\"demo12345\",\"terms_revision\":\"$REV\"}")
USER_TOKEN=$(jqget "$USER_RESP" '.data.token')
if [[ -z "$USER_TOKEN" || "$USER_TOKEN" == "null" ]]; then
  # 已注册过则直接登录。
  USER_RESP=$(curl -s "$BASE/api/auth/login" -d "{\"email\":\"demo@dengdeng.local\",\"password\":\"demo12345\",\"terms_revision\":\"$REV\"}")
  USER_TOKEN=$(jqget "$USER_RESP" '.data.token')
fi
[[ -n "$USER_TOKEN" && "$USER_TOKEN" != "null" ]] || { echo "demo 用户登录失败: $USER_RESP"; exit 1; }
USER_ID=$(jqget "$USER_RESP" '.data.user.id')
curl -s -X PUT "$BASE/api/admin/users/$USER_ID" -H "$AH" -d '{"add_balance_micro":20000000}' >/dev/null
UH="Authorization: Bearer $USER_TOKEN"

echo "==> 为四个平台各建一个 API Key(写入 $OUT)"
mkdir -p "$(dirname "$OUT")"
: >"$OUT"
for P in anthropic openai gemini grok; do
  GID_VAR="GID_$P"
  K=$(curl -s "$BASE/api/user/keys" -H "$UH" -d "{\"name\":\"key-$P\",\"group_id\":${!GID_VAR}}")
  PLAIN=$(jqget "$K" '.data.plain')
  [[ -n "$PLAIN" && "$PLAIN" != "null" ]] || { echo "创建密钥失败[$P]: $K"; exit 1; }
  eval "KEY_$P=$PLAIN"
  echo "$P: $PLAIN" >>"$OUT"
  echo "    $P -> ${PLAIN:0:8}****"
done

echo "==> 产生转发流量(每平台 流式+非流式 x5)"
for i in 1 2 3 4 5; do
  curl -s "$BASE/v1/messages" -H "x-api-key: $KEY_anthropic" -H 'content-type: application/json' \
    -d '{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}' >/dev/null
  curl -s -N "$BASE/v1/messages" -H "x-api-key: $KEY_anthropic" -H 'content-type: application/json' \
    -d '{"model":"claude-opus-4-1","stream":true,"messages":[{"role":"user","content":"hi"}]}' >/dev/null
  curl -s "$BASE/v1/chat/completions" -H "Authorization: Bearer $KEY_openai" -H 'content-type: application/json' \
    -d '{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}' >/dev/null
  curl -s -N "$BASE/v1/chat/completions" -H "Authorization: Bearer $KEY_openai" -H 'content-type: application/json' \
    -d '{"model":"gpt-4o-mini","stream":true,"messages":[{"role":"user","content":"hi"}]}' >/dev/null
  curl -s "$BASE/v1beta/models/gemini-2.5-pro:generateContent" -H "x-goog-api-key: $KEY_gemini" \
    -H 'content-type: application/json' -d '{"contents":[{"parts":[{"text":"hi"}]}]}' >/dev/null
  curl -s -N "$BASE/v1beta/models/gemini-2.5-flash:streamGenerateContent?alt=sse&key=$KEY_gemini" \
    -H 'content-type: application/json' -d '{"contents":[{"parts":[{"text":"hi"}]}]}' >/dev/null
  curl -s "$BASE/v1/chat/completions" -H "Authorization: Bearer $KEY_grok" -H 'content-type: application/json' \
    -d '{"model":"grok-4.5","messages":[{"role":"user","content":"hi"}]}' >/dev/null
  curl -s -N "$BASE/v1/chat/completions" -H "Authorization: Bearer $KEY_grok" -H 'content-type: application/json' \
    -d '{"model":"grok-composer-2.5-fast","stream":true,"messages":[{"role":"user","content":"hi"}]}' >/dev/null
done

sleep 1
ME=$(curl -s "$BASE/api/user/me" -H "$UH")
BAL=$(jqget "$ME" '.data.balance_micro')
echo "==> 完成。demo 用户余额(micro-USD): $BAL / 20000000"
echo "    控制台: $BASE  管理员: $ADMIN_EMAIL / $ADMIN_PASSWORD  用户: demo@dengdeng.local / demo12345"
