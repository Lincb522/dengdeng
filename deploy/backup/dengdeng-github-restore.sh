#!/usr/bin/env bash
set -Eeuo pipefail

umask 077

if (( $# < 2 || $# > 3 )); then
  echo "用法：$0 <备份仓库目录> [快照时间或 latest] <输出数据库>" >&2
  exit 2
fi

REPOSITORY_DIRECTORY="$(cd "$1" && pwd)"
if (( $# == 2 )); then
  SNAPSHOT_NAME="latest"
  OUTPUT_DATABASE="$2"
else
  SNAPSHOT_NAME="$2"
  OUTPUT_DATABASE="$3"
fi

PASSPHRASE_FILE="${DENGDENG_BACKUP_PASSPHRASE_FILE:-/etc/dengdeng/github-backup.pass}"
[[ -r "$PASSPHRASE_FILE" ]] || { echo "无法读取恢复密钥：$PASSPHRASE_FILE" >&2; exit 1; }
[[ ! -e "$OUTPUT_DATABASE" ]] || { echo "输出文件已存在，拒绝覆盖：$OUTPUT_DATABASE" >&2; exit 1; }

if [[ "$SNAPSHOT_NAME" == "latest" ]]; then
  SNAPSHOT_NAME="$(tr -d '\r\n' < "$REPOSITORY_DIRECTORY/latest-database")"
fi
[[ "$SNAPSHOT_NAME" =~ ^[0-9]{8}T[0-9]{6}Z$ ]] || { echo "快照名称无效" >&2; exit 1; }

SNAPSHOT_DIRECTORY="$REPOSITORY_DIRECTORY/database/$SNAPSHOT_NAME"
MANIFEST="$SNAPSHOT_DIRECTORY/manifest"
[[ -r "$MANIFEST" ]] || { echo "找不到快照清单：$SNAPSHOT_NAME" >&2; exit 1; }

EXPECTED_SHA256="$(awk -F= '$1 == "encrypted_sha256" { print $2 }' "$MANIFEST")"
[[ "$EXPECTED_SHA256" =~ ^[0-9a-f]{64}$ ]] || { echo "快照清单校验值无效" >&2; exit 1; }

TEMP_DIRECTORY="$(mktemp -d)"
trap 'rm -rf "$TEMP_DIRECTORY"; rm -f "$OUTPUT_DATABASE"' ERR
trap 'rm -rf "$TEMP_DIRECTORY"' EXIT
ENCRYPTED_SNAPSHOT="$TEMP_DIRECTORY/dengdeng.sqlite.gz.gpg"
COMPRESSED_SNAPSHOT="$TEMP_DIRECTORY/dengdeng.sqlite.gz"

find "$SNAPSHOT_DIRECTORY" -maxdepth 1 -type f -name 'part-*' -print0 \
  | sort -z \
  | xargs -0 cat > "$ENCRYPTED_SNAPSHOT"
ACTUAL_SHA256="$(sha256sum "$ENCRYPTED_SNAPSHOT" | awk '{print $1}')"
[[ "$ACTUAL_SHA256" == "$EXPECTED_SHA256" ]] || { echo "加密快照校验失败" >&2; exit 1; }

GNUPGHOME="$TEMP_DIRECTORY/gnupg"
mkdir -m 0700 "$GNUPGHOME"
export GNUPGHOME
gpg --batch --yes --quiet \
  --decrypt \
  --pinentry-mode loopback \
  --passphrase-file "$PASSPHRASE_FILE" \
  --output "$COMPRESSED_SNAPSHOT" \
  "$ENCRYPTED_SNAPSHOT"
gzip -dc "$COMPRESSED_SNAPSHOT" > "$OUTPUT_DATABASE"

INTEGRITY_RESULT="$(sqlite3 "$OUTPUT_DATABASE" 'PRAGMA integrity_check;')"
[[ "$INTEGRITY_RESULT" == "ok" ]] || { echo "恢复数据库完整性检查失败：$INTEGRITY_RESULT" >&2; exit 1; }
chmod 0600 "$OUTPUT_DATABASE"
echo "恢复校验完成：$OUTPUT_DATABASE"
