#!/usr/bin/env bash
set -Eeuo pipefail

umask 077

CONFIG_FILE="${DENGDENG_GITHUB_BACKUP_CONFIG:-/etc/dengdeng/github-backup.conf}"
[[ -r "$CONFIG_FILE" ]] || { echo "无法读取备份配置：$CONFIG_FILE" >&2; exit 1; }
# shellcheck disable=SC1090
source "$CONFIG_FILE"

: "${DATABASE_PATH:?缺少 DATABASE_PATH}"
: "${BACKUP_REPOSITORY:?缺少 BACKUP_REPOSITORY}"
: "${BACKUP_IDENTITY_FILE:?缺少 BACKUP_IDENTITY_FILE}"
: "${BACKUP_KNOWN_HOSTS_FILE:?缺少 BACKUP_KNOWN_HOSTS_FILE}"
: "${BACKUP_PASSPHRASE_FILE:?缺少 BACKUP_PASSPHRASE_FILE}"

BACKUP_WORK_DIRECTORY="${BACKUP_WORK_DIRECTORY:-/var/lib/dengdeng/github-backup}"
BACKUP_RETENTION_COUNT="${BACKUP_RETENTION_COUNT:-7}"
BACKUP_FULLSTACK_ENABLED="${BACKUP_FULLSTACK_ENABLED:-true}"
BACKUP_FULLSTACK_RETENTION_COUNT="${BACKUP_FULLSTACK_RETENTION_COUNT:-2}"
BACKUP_PART_SIZE="${BACKUP_PART_SIZE:-90m}"
BACKUP_MAX_REPOSITORY_KIB="${BACKUP_MAX_REPOSITORY_KIB:-921600}"
BACKUP_BRANCH="${BACKUP_BRANCH:-main}"
FULLSTACK_SOURCE_DIRECTORY="${FULLSTACK_SOURCE_DIRECTORY:-/opt/dengdeng/source}"
FULLSTACK_BINARY="${FULLSTACK_BINARY:-/opt/dengdeng/dengdeng}"
FULLSTACK_ENV_FILE="${DENGDENG_FULLSTACK_ENV_FILE:-${FULLSTACK_ENV_FILE:-/etc/dengdeng/dengdeng.env}}"
FULLSTACK_UPDATE_CONFIG="${DENGDENG_FULLSTACK_UPDATE_CONFIG:-${FULLSTACK_UPDATE_CONFIG:-/etc/dengdeng/update.conf}}"

[[ "$BACKUP_RETENTION_COUNT" =~ ^[1-9][0-9]*$ ]] || { echo "BACKUP_RETENTION_COUNT 必须是正整数" >&2; exit 1; }
[[ "$BACKUP_FULLSTACK_RETENTION_COUNT" =~ ^[1-9][0-9]*$ ]] || { echo "BACKUP_FULLSTACK_RETENTION_COUNT 必须是正整数" >&2; exit 1; }
[[ "$BACKUP_MAX_REPOSITORY_KIB" =~ ^[1-9][0-9]*$ ]] || { echo "BACKUP_MAX_REPOSITORY_KIB 必须是正整数" >&2; exit 1; }

for command_name in sqlite3 gzip gpg git ssh sha256sum split flock tar; do
  command -v "$command_name" >/dev/null 2>&1 || { echo "缺少命令：$command_name" >&2; exit 1; }
done

for required_file in "$DATABASE_PATH" "$BACKUP_IDENTITY_FILE" "$BACKUP_KNOWN_HOSTS_FILE" "$BACKUP_PASSPHRASE_FILE"; do
  [[ -r "$required_file" ]] || { echo "无法读取：$required_file" >&2; exit 1; }
done

DATABASE_ARCHIVE_DIRECTORY="$BACKUP_WORK_DIRECTORY/archive/database"
FULLSTACK_ARCHIVE_DIRECTORY="$BACKUP_WORK_DIRECTORY/archive/fullstack"
mkdir -p "$DATABASE_ARCHIVE_DIRECTORY" "$FULLSTACK_ARCHIVE_DIRECTORY"

# Migrate snapshots created before the repository gained separate database and
# full-stack namespaces.
while IFS= read -r legacy_snapshot; do
  mv "$legacy_snapshot" "$DATABASE_ARCHIVE_DIRECTORY/"
done < <(find "$BACKUP_WORK_DIRECTORY/archive" -mindepth 1 -maxdepth 1 -type d -name '20*T*Z' | sort)

exec 9>"$BACKUP_WORK_DIRECTORY/backup.lock"
flock -n 9 || { echo "已有 GitHub 备份任务正在运行" >&2; exit 0; }

TEMP_DIRECTORY="$(mktemp -d "$BACKUP_WORK_DIRECTORY/.backup.XXXXXX")"
trap 'rm -rf "$TEMP_DIRECTORY"' EXIT

TIMESTAMP="$(date -u +%Y%m%dT%H%M%SZ)"
CREATED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
RAW_SNAPSHOT="$TEMP_DIRECTORY/dengdeng.sqlite"
GNUPGHOME="$TEMP_DIRECTORY/gnupg"
mkdir -m 0700 "$GNUPGHOME"
export GNUPGHOME

sqlite3 "$DATABASE_PATH" "VACUUM INTO '$RAW_SNAPSHOT';"
INTEGRITY_RESULT="$(sqlite3 "$RAW_SNAPSHOT" 'PRAGMA integrity_check;')"
[[ "$INTEGRITY_RESULT" == "ok" ]] || { echo "SQLite 快照完整性检查失败：$INTEGRITY_RESULT" >&2; exit 1; }

encrypt_and_split() {
  local input_file="$1"
  local snapshot_directory="$2"
  local kind="$3"
  local payload_format="$4"
  local encrypted_file="$TEMP_DIRECTORY/$kind.payload.gpg"

  gpg --batch --yes --quiet \
    --symmetric --cipher-algo AES256 \
    --pinentry-mode loopback \
    --passphrase-file "$BACKUP_PASSPHRASE_FILE" \
    --output "$encrypted_file" \
    "$input_file"

  mkdir "$snapshot_directory"
  split -b "$BACKUP_PART_SIZE" -d -a 3 "$encrypted_file" "$snapshot_directory/part-"

  local encrypted_sha256 encrypted_bytes part_count
  encrypted_sha256="$(sha256sum "$encrypted_file" | awk '{print $1}')"
  encrypted_bytes="$(wc -c < "$encrypted_file" | tr -d ' ')"
  part_count="$(find "$snapshot_directory" -maxdepth 1 -type f -name 'part-*' | wc -l | tr -d ' ')"
  cat > "$snapshot_directory/manifest" <<EOF
format=2
kind=$kind
payload_format=$payload_format
created_at=$CREATED_AT
encrypted_sha256=$encrypted_sha256
encrypted_bytes=$encrypted_bytes
parts=$part_count
encryption=gpg-aes256
EOF
  printf '%s:%s:%s\n' "$kind" "$part_count" "$encrypted_bytes"
}

prune_snapshots() {
  local directory="$1"
  local retention="$2"
  local -a snapshots
  mapfile -t snapshots < <(find "$directory" -mindepth 1 -maxdepth 1 -type d | sort)
  while (( ${#snapshots[@]} > retention )); do
    rm -rf "${snapshots[0]}"
    snapshots=("${snapshots[@]:1}")
  done
}

DATABASE_COMPRESSED="$TEMP_DIRECTORY/dengdeng.sqlite.gz"
gzip -9 -c "$RAW_SNAPSHOT" > "$DATABASE_COMPRESSED"
DATABASE_RESULT="$(encrypt_and_split "$DATABASE_COMPRESSED" "$DATABASE_ARCHIVE_DIRECTORY/$TIMESTAMP" database sqlite-gzip)"

FULLSTACK_RESULT="disabled"
if [[ "$BACKUP_FULLSTACK_ENABLED" == "true" ]]; then
  [[ -d "$FULLSTACK_SOURCE_DIRECTORY/.git" ]] || { echo "全栈源码仓库不可用：$FULLSTACK_SOURCE_DIRECTORY" >&2; exit 1; }
  [[ -r "$FULLSTACK_BINARY" ]] || { echo "运行二进制不可读：$FULLSTACK_BINARY" >&2; exit 1; }
  [[ -r "$FULLSTACK_ENV_FILE" ]] || { echo "运行环境配置不可读：$FULLSTACK_ENV_FILE" >&2; exit 1; }

  PAYLOAD_DIRECTORY="$TEMP_DIRECTORY/fullstack"
  mkdir -p \
    "$PAYLOAD_DIRECTORY/database" \
    "$PAYLOAD_DIRECTORY/source" \
    "$PAYLOAD_DIRECTORY/runtime" \
    "$PAYLOAD_DIRECTORY/configuration/nginx/conf.d" \
    "$PAYLOAD_DIRECTORY/configuration/systemd" \
    "$PAYLOAD_DIRECTORY/configuration/maintenance"

  SANITIZED_DATABASE="$PAYLOAD_DIRECTORY/database/dengdeng.sanitized.db"
  cp "$RAW_SNAPSHOT" "$SANITIZED_DATABASE"
  sqlite3 "$SANITIZED_DATABASE" <<'SQL'
PRAGMA foreign_keys=OFF;
BEGIN IMMEDIATE;
UPDATE payment_configs
SET enabled = 0,
    credit_micro_per_unit = 0,
    daily_limit_minor = 0;
DELETE FROM payment_provider_instances;
UPDATE payment_orders
SET provider_id = 0,
    provider_key = 'removed',
    payment_method = 'removed',
    provider_trade_no = '',
    provider_snapshot = '',
    checkout_data = '',
    failure_reason = '',
    refund_trade_no = '';
DELETE FROM payment_audit_logs;
UPDATE payment_ledger_entries
SET provider_key = '',
    payment_method = '';
DELETE FROM referral_payout_accounts;
UPDATE referral_payout_configs
SET enabled = 0,
    wx_provider_id = 0,
    wx_transfer_scene_id = '',
    scene_report_info_type = '',
    scene_report_info_content = '',
    transfer_remark = '推广佣金';
UPDATE referral_payouts
SET payout_account_id = 0,
    provider_id = 0,
    channel = 'removed',
    provider_bill_no = '',
    app_id = '',
    merchant_id = '',
    package_info = '',
    failure_code = '',
    failure_message = '';
COMMIT;
VACUUM;
SQL

  SANITIZED_INTEGRITY="$(sqlite3 "$SANITIZED_DATABASE" 'PRAGMA integrity_check;')"
  [[ "$SANITIZED_INTEGRITY" == "ok" ]] || { echo "脱敏数据库完整性检查失败：$SANITIZED_INTEGRITY" >&2; exit 1; }
  [[ "$(sqlite3 "$SANITIZED_DATABASE" 'SELECT COUNT(*) FROM payment_provider_instances;')" == "0" ]] || { echo "支付渠道未完全移除" >&2; exit 1; }
  [[ "$(sqlite3 "$SANITIZED_DATABASE" 'SELECT COUNT(*) FROM referral_payout_accounts;')" == "0" ]] || { echo "提现绑定未完全移除" >&2; exit 1; }
  [[ "$(sqlite3 "$SANITIZED_DATABASE" 'SELECT COUNT(*) FROM payment_configs WHERE enabled <> 0 OR credit_micro_per_unit <> 0;')" == "0" ]] || { echo "支付配置未完全停用" >&2; exit 1; }

  git -c safe.directory="$FULLSTACK_SOURCE_DIRECTORY" -C "$FULLSTACK_SOURCE_DIRECTORY" archive HEAD | tar -xf - -C "$PAYLOAD_DIRECTORY/source"
  cp "$FULLSTACK_BINARY" "$PAYLOAD_DIRECTORY/runtime/dengdeng"
  chmod 0755 "$PAYLOAD_DIRECTORY/runtime/dengdeng"

  awk -F= '
    /^[A-Za-z_][A-Za-z0-9_]*=/ {
      key = toupper($1)
      if (key ~ /(PAYMENT|WXPAY|WECHAT_PAY|MCH_|MERCHANT|STRIPE|AIRWALLEX|ALIPAY|API_?V3)/) {
        print $1 "=REMOVED"
        next
      }
    }
    { print }
  ' "$FULLSTACK_ENV_FILE" > "$PAYLOAD_DIRECTORY/configuration/dengdeng.env"

  if [[ -r "$FULLSTACK_UPDATE_CONFIG" ]]; then
    cp "$FULLSTACK_UPDATE_CONFIG" "$PAYLOAD_DIRECTORY/configuration/update.conf"
  fi
  if [[ -r /etc/nginx/nginx.conf ]]; then
    cp /etc/nginx/nginx.conf "$PAYLOAD_DIRECTORY/configuration/nginx/nginx.conf"
  fi
  for nginx_config in /etc/nginx/conf.d/*.conf; do
    [[ -r "$nginx_config" ]] || continue
    cp "$nginx_config" "$PAYLOAD_DIRECTORY/configuration/nginx/conf.d/"
  done
  for systemd_unit in /etc/systemd/system/dengdeng*.service /etc/systemd/system/dengdeng*.timer; do
    [[ -r "$systemd_unit" ]] || continue
    cp "$systemd_unit" "$PAYLOAD_DIRECTORY/configuration/systemd/"
  done
  for maintenance_script in /usr/local/sbin/dengdeng-github-backup /usr/local/sbin/dengdeng-github-restore /usr/local/sbin/dengdeng-fullstack-restore /usr/local/sbin/dengdeng-update; do
    [[ -r "$maintenance_script" ]] || continue
    cp "$maintenance_script" "$PAYLOAD_DIRECTORY/configuration/maintenance/"
  done

  SOURCE_COMMIT="$(git -c safe.directory="$FULLSTACK_SOURCE_DIRECTORY" -C "$FULLSTACK_SOURCE_DIRECTORY" rev-parse HEAD)"
  cat > "$PAYLOAD_DIRECTORY/RESTORE-NOTES.txt" <<EOF
DengDeng AI 脱敏全栈快照
创建时间：$CREATED_AT
源码提交：$SOURCE_COMMIT

已移除：
- 所有支付渠道及商户密钥配置
- 支付订单中的渠道快照、外部流水号与收银台数据
- 推广提现 OpenID、微信商户和转账场景绑定
- 环境文件中名称匹配支付渠道的变量
- TLS 私钥、GitHub deploy key 和备份解密口令

恢复后在线支付和推广提现保持关闭，需要管理员重新绑定渠道。
EOF

  FULLSTACK_COMPRESSED="$TEMP_DIRECTORY/dengdeng-fullstack.tar.gz"
  tar -C "$PAYLOAD_DIRECTORY" -czf "$FULLSTACK_COMPRESSED" .
  FULLSTACK_RESULT="$(encrypt_and_split "$FULLSTACK_COMPRESSED" "$FULLSTACK_ARCHIVE_DIRECTORY/$TIMESTAMP" fullstack tar-gzip)"
fi

prune_snapshots "$DATABASE_ARCHIVE_DIRECTORY" "$BACKUP_RETENTION_COUNT"
prune_snapshots "$FULLSTACK_ARCHIVE_DIRECTORY" "$BACKUP_FULLSTACK_RETENTION_COUNT"

ARCHIVE_KIB="$(du -sk "$BACKUP_WORK_DIRECTORY/archive" | awk '{print $1}')"
if (( ARCHIVE_KIB > BACKUP_MAX_REPOSITORY_KIB )); then
  echo "加密备份总量 ${ARCHIVE_KIB} KiB 超过仓库上限 ${BACKUP_MAX_REPOSITORY_KIB} KiB，已停止推送" >&2
  exit 1
fi

REPOSITORY_DIRECTORY="$TEMP_DIRECTORY/repository"
mkdir "$REPOSITORY_DIRECTORY"
cp -a "$BACKUP_WORK_DIRECTORY/archive/." "$REPOSITORY_DIRECTORY/"
printf '%s\n' "$TIMESTAMP" > "$REPOSITORY_DIRECTORY/latest-database"
if [[ "$BACKUP_FULLSTACK_ENABLED" == "true" ]]; then
  printf '%s\n' "$TIMESTAMP" > "$REPOSITORY_DIRECTORY/latest-fullstack"
fi
cat > "$REPOSITORY_DIRECTORY/README.md" <<'EOF'
# DengDeng AI 加密备份

此仓库只保存经过 GPG AES-256 加密的快照，不包含解密口令。

- `database/`：完整 SQLite 一致性快照。
- `fullstack/`：源码、运行二进制、部署配置和脱敏数据库；支付渠道、商户密钥、收款/提现绑定均已移除。
- `latest-database` 与 `latest-fullstack`：对应类型的最新快照。

恢复时使用 DengDeng 主仓库 `deploy/backup/` 中的恢复脚本，先输出到新路径并完成完整性检查，禁止直接覆盖运行中的生产文件。
EOF

git -C "$REPOSITORY_DIRECTORY" init --quiet --initial-branch="$BACKUP_BRANCH"
git -C "$REPOSITORY_DIRECTORY" config user.name "DengDeng Backup"
git -C "$REPOSITORY_DIRECTORY" config user.email "backup@dengdeng.local"
git -C "$REPOSITORY_DIRECTORY" add --all
git -C "$REPOSITORY_DIRECTORY" commit --quiet -m "backup: $CREATED_AT"
git -C "$REPOSITORY_DIRECTORY" remote add origin "$BACKUP_REPOSITORY"

export GIT_SSH_COMMAND="ssh -i $BACKUP_IDENTITY_FILE -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=$BACKUP_KNOWN_HOSTS_FILE"
git -C "$REPOSITORY_DIRECTORY" push --quiet --force origin "$BACKUP_BRANCH"

echo "GitHub 加密备份完成：$TIMESTAMP（$DATABASE_RESULT，fullstack=$FULLSTACK_RESULT）"
