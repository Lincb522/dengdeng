#!/usr/bin/env bash
set -Eeuo pipefail

CONFIG_FILE="${DENGDENG_UPDATE_CONFIG:-/etc/dengdeng/update.conf}"
REPOSITORY="https://github.com/Lincb522/dengdeng.git"
BRANCH="main"
SOURCE_DIRECTORY="/opt/dengdeng/source"
RELEASE_DIRECTORY="/opt/dengdeng/releases"
RUNTIME_BINARY="/opt/dengdeng/dengdeng"
SERVICE="dengdeng.service"
HEALTH_URL="http://127.0.0.1:9100/health"
STATE_DIRECTORY="/var/lib/dengdeng/update"
BUILD_JOBS="2"
GOPROXY="https://proxy.golang.org,direct"

if [[ -r "$CONFIG_FILE" ]]; then
  while IFS='=' read -r key value; do
    key="${key//[[:space:]]/}"
    value="${value#${value%%[![:space:]]*}}"
    value="${value%${value##*[![:space:]]}}"
    [[ -z "$key" || "$key" == \#* ]] && continue
    case "$key" in
      REPOSITORY) REPOSITORY="$value" ;;
      BRANCH) BRANCH="$value" ;;
      SOURCE_DIRECTORY) SOURCE_DIRECTORY="$value" ;;
      RELEASE_DIRECTORY) RELEASE_DIRECTORY="$value" ;;
      RUNTIME_BINARY) RUNTIME_BINARY="$value" ;;
      SERVICE) SERVICE="$value" ;;
      HEALTH_URL) HEALTH_URL="$value" ;;
      STATE_DIRECTORY) STATE_DIRECTORY="$value" ;;
      BUILD_JOBS) BUILD_JOBS="$value" ;;
      GOPROXY) GOPROXY="$value" ;;
    esac
  done < "$CONFIG_FILE"
fi

REQUEST_FILE="$STATE_DIRECTORY/request.json"
STATUS_FILE="$STATE_DIRECTORY/status.json"
LOCK_FILE="$STATE_DIRECTORY/update.lock"
CURRENT_COMMIT_FILE="$RELEASE_DIRECTORY/CURRENT_COMMIT"
CURRENT_VERSION_FILE="$RELEASE_DIRECTORY/CURRENT_VERSION"
PREVIOUS_COMMIT_FILE="$RELEASE_DIRECTORY/PREVIOUS_COMMIT"
PREVIOUS_BINARY="$RELEASE_DIRECTORY/dengdeng.previous"
HOME_DIRECTORY="/opt/dengdeng/.update-home"

for path in "$SOURCE_DIRECTORY" "$RELEASE_DIRECTORY" "$RUNTIME_BINARY" "$STATE_DIRECTORY"; do
  [[ "$path" == /* ]] || { echo "update paths must be absolute" >&2; exit 2; }
done
[[ "$BRANCH" =~ ^[A-Za-z0-9._/-]+$ && "$BRANCH" != -* && "$BRANCH" != *".."* ]] || { echo "invalid branch" >&2; exit 2; }
[[ "$SERVICE" =~ ^[A-Za-z0-9_.@-]+\.service$ ]] || { echo "invalid service" >&2; exit 2; }
[[ "$BUILD_JOBS" =~ ^[1-9][0-9]?$ ]] || BUILD_JOBS=2

install -d -m 0750 -o dengdeng -g dengdeng "$STATE_DIRECTORY"
install -d -m 0750 "$SOURCE_DIRECTORY" "$RELEASE_DIRECTORY" "$HOME_DIRECTORY"
exec 9>"$LOCK_FILE"
flock -n 9 || exit 0

ACTION=""
REQUESTED_BY=""
REQUESTED_AT=""
STATUS="running"
STAGE="initializing"
MESSAGE="正在准备更新任务"
CURRENT_COMMIT=""
CURRENT_VERSION=""
TARGET_COMMIT=""
PREVIOUS_COMMIT=""
UPDATE_AVAILABLE="false"
STARTED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
FINISHED_AT=""
SWITCHED="false"
RESTORE_BINARY=""
RESTORE_COMMIT=""
RESTORE_VERSION=""

json_field() {
  python3 - "$REQUEST_FILE" "$1" <<'PY'
import json, sys
try:
    with open(sys.argv[1], encoding="utf-8") as handle:
        value = json.load(handle).get(sys.argv[2], "")
except (OSError, ValueError, TypeError):
    value = ""
print(value if isinstance(value, str) else "")
PY
}

load_markers() {
  [[ -r "$CURRENT_COMMIT_FILE" ]] && CURRENT_COMMIT="$(tr -d '\r\n' < "$CURRENT_COMMIT_FILE")"
  [[ -r "$CURRENT_VERSION_FILE" ]] && CURRENT_VERSION="$(tr -d '\r\n' < "$CURRENT_VERSION_FILE")"
  [[ -r "$PREVIOUS_COMMIT_FILE" ]] && PREVIOUS_COMMIT="$(tr -d '\r\n' < "$PREVIOUS_COMMIT_FILE")"
  if [[ -z "$CURRENT_COMMIT" ]]; then
    local health_json
    health_json="$(curl --fail --silent --max-time 3 "$HEALTH_URL" 2>/dev/null || true)"
    if [[ -n "$health_json" ]]; then
      CURRENT_COMMIT="$(python3 -c 'import json,sys; print(json.load(sys.stdin).get("commit", ""))' <<<"$health_json" 2>/dev/null || true)"
      CURRENT_VERSION="$(python3 -c 'import json,sys; print(json.load(sys.stdin).get("version", ""))' <<<"$health_json" 2>/dev/null || true)"
      [[ -n "$CURRENT_COMMIT" ]] && printf '%s\n' "$CURRENT_COMMIT" > "$CURRENT_COMMIT_FILE"
      [[ -n "$CURRENT_VERSION" ]] && printf '%s\n' "$CURRENT_VERSION" > "$CURRENT_VERSION_FILE"
    fi
  fi
}

write_state() {
  export DD_UPDATE_REPOSITORY="$REPOSITORY" DD_UPDATE_BRANCH="$BRANCH"
  export DD_UPDATE_STATUS="$STATUS" DD_UPDATE_ACTION="$ACTION" DD_UPDATE_STAGE="$STAGE" DD_UPDATE_MESSAGE="$MESSAGE"
  export DD_UPDATE_CURRENT_VERSION="$CURRENT_VERSION" DD_UPDATE_CURRENT_COMMIT="$CURRENT_COMMIT"
  export DD_UPDATE_TARGET_COMMIT="$TARGET_COMMIT" DD_UPDATE_PREVIOUS_COMMIT="$PREVIOUS_COMMIT"
  export DD_UPDATE_AVAILABLE="$UPDATE_AVAILABLE" DD_UPDATE_REQUESTED_BY="$REQUESTED_BY" DD_UPDATE_REQUESTED_AT="$REQUESTED_AT"
  export DD_UPDATE_STARTED_AT="$STARTED_AT" DD_UPDATE_FINISHED_AT="$FINISHED_AT" DD_UPDATE_STATUS_FILE="$STATUS_FILE"
  python3 <<'PY'
import json, os, pathlib
path = pathlib.Path(os.environ["DD_UPDATE_STATUS_FILE"])
data = {
    "enabled": True,
    "repository": os.environ["DD_UPDATE_REPOSITORY"],
    "branch": os.environ["DD_UPDATE_BRANCH"],
    "status": os.environ["DD_UPDATE_STATUS"],
    "action": os.environ["DD_UPDATE_ACTION"],
    "stage": os.environ["DD_UPDATE_STAGE"],
    "message": os.environ["DD_UPDATE_MESSAGE"],
    "current_version": os.environ["DD_UPDATE_CURRENT_VERSION"],
    "current_commit": os.environ["DD_UPDATE_CURRENT_COMMIT"],
    "target_commit": os.environ["DD_UPDATE_TARGET_COMMIT"],
    "previous_commit": os.environ["DD_UPDATE_PREVIOUS_COMMIT"],
    "update_available": os.environ["DD_UPDATE_AVAILABLE"] == "true",
    "can_rollback": bool(os.environ["DD_UPDATE_PREVIOUS_COMMIT"]),
    "requested_by": os.environ["DD_UPDATE_REQUESTED_BY"],
    "requested_at": os.environ["DD_UPDATE_REQUESTED_AT"],
    "started_at": os.environ["DD_UPDATE_STARTED_AT"],
    "finished_at": os.environ["DD_UPDATE_FINISHED_AT"],
}
temporary = path.with_suffix(".tmp")
temporary.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
temporary.chmod(0o640)
os.replace(temporary, path)
PY
  chown dengdeng:dengdeng "$STATUS_FILE"
}

set_stage() {
  STAGE="$1"
  MESSAGE="$2"
  write_state
}

healthy() {
  local attempt
  for attempt in $(seq 1 30); do
    if curl --fail --silent --max-time 3 "$HEALTH_URL" >/dev/null; then
      return 0
    fi
    sleep 1
  done
  return 1
}

restore_after_failure() {
  set +e
  if [[ "$SWITCHED" == "true" && -n "$RESTORE_BINARY" && -x "$RESTORE_BINARY" ]]; then
    install -m 0755 -o dengdeng -g dengdeng "$RESTORE_BINARY" "$RUNTIME_BINARY"
    printf '%s\n' "$RESTORE_COMMIT" > "$CURRENT_COMMIT_FILE"
    printf '%s\n' "$RESTORE_VERSION" > "$CURRENT_VERSION_FILE"
    systemctl restart "$SERVICE"
    healthy
    CURRENT_COMMIT="$RESTORE_COMMIT"
    CURRENT_VERSION="$RESTORE_VERSION"
    MESSAGE="更新失败，已自动恢复上一运行版本"
  else
    MESSAGE="更新任务失败，线上版本未切换"
  fi
  STATUS="failed"
  FINISHED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  write_state
}

on_error() {
  local code=$?
  trap - ERR
  git -C "$SOURCE_DIRECTORY" restore -- backend/internal/web/dist/index.html 2>/dev/null || true
  restore_after_failure
  exit "$code"
}
trap on_error ERR

prepare_repository() {
  set_stage "fetching" "正在同步远程仓库"
  if [[ ! -d "$SOURCE_DIRECTORY/.git" ]]; then
    rm -rf "$SOURCE_DIRECTORY"
    git clone --filter=blob:none --no-tags --branch "$BRANCH" "$REPOSITORY" "$SOURCE_DIRECTORY"
  fi
  git -C "$SOURCE_DIRECTORY" remote set-url origin "$REPOSITORY"
  git -C "$SOURCE_DIRECTORY" fetch --prune --tags origin "+refs/heads/$BRANCH:refs/remotes/origin/$BRANCH"
  TARGET_COMMIT="$(git -C "$SOURCE_DIRECTORY" rev-parse --verify "origin/$BRANCH^{commit}")"
  [[ "$TARGET_COMMIT" =~ ^[0-9a-f]{40}$ ]]
  if [[ -n "$CURRENT_COMMIT" && "$CURRENT_COMMIT" == "$TARGET_COMMIT" ]]; then
    UPDATE_AVAILABLE="false"
  else
    UPDATE_AVAILABLE="true"
  fi
}

build_release() {
  set_stage "building_frontend" "正在构建管理端"
  git -C "$SOURCE_DIRECTORY" checkout -f -B "$BRANCH" "origin/$BRANCH"
  git -C "$SOURCE_DIRECTORY" reset --hard "$TARGET_COMMIT"
  git -C "$SOURCE_DIRECTORY" clean -ffd
  export HOME="$HOME_DIRECTORY"
  export PNPM_HOME="$HOME_DIRECTORY/pnpm"
  export GOPROXY
  export PATH="$PNPM_HOME:/usr/local/bin:/usr/bin:/bin"
  cd "$SOURCE_DIRECTORY/frontend"
  pnpm install --frozen-lockfile --prefer-offline
  pnpm build

  set_stage "building_backend" "正在构建服务端"
  local release version_name build_time ldflags
  release="$RELEASE_DIRECTORY/dengdeng-$TARGET_COMMIT"
  version_name="$(git -C "$SOURCE_DIRECTORY" describe --tags --always "$TARGET_COMMIT")"
  build_time="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  ldflags="-s -w -X dengdeng/internal/version.Version=$version_name -X dengdeng/internal/version.Commit=$TARGET_COMMIT -X dengdeng/internal/version.BuildTime=$build_time"
  cd "$SOURCE_DIRECTORY/backend"
  GOMAXPROCS="$BUILD_JOBS" CGO_ENABLED=0 go build -p "$BUILD_JOBS" -trimpath -ldflags "$ldflags" -o "$release.tmp" ./cmd/server
  git -C "$SOURCE_DIRECTORY" restore -- backend/internal/web/dist/index.html
  chmod 0755 "$release.tmp"
  mv -f "$release.tmp" "$release"
  TARGET_VERSION="$version_name"
}

sync_updater_components() {
  set_stage "health_check" "正在同步更新组件"
  bash -n "$SOURCE_DIRECTORY/deploy/update/dengdeng-update.sh" "$SOURCE_DIRECTORY/deploy/update/install.sh"
  install -m 0755 "$SOURCE_DIRECTORY/deploy/update/dengdeng-update.sh" /usr/local/sbin/dengdeng-update.next
  mv -f /usr/local/sbin/dengdeng-update.next /usr/local/sbin/dengdeng-update
  install -m 0644 "$SOURCE_DIRECTORY/deploy/systemd/dengdeng-updater.service" /etc/systemd/system/dengdeng-updater.service
  install -m 0644 "$SOURCE_DIRECTORY/deploy/polkit/49-dengdeng-updater.rules" /etc/polkit-1/rules.d/49-dengdeng-updater.rules
  systemctl daemon-reload
}

apply_release() {
  if [[ "$UPDATE_AVAILABLE" != "true" ]]; then
    STATUS="succeeded"
    STAGE="ready"
    MESSAGE="当前已经是仓库最新版本"
    FINISHED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    write_state
    return
  fi
  build_release
  set_stage "switching" "正在切换服务版本"
  RESTORE_BINARY="$RELEASE_DIRECTORY/dengdeng.restore.$TARGET_COMMIT"
  RESTORE_COMMIT="$CURRENT_COMMIT"
  RESTORE_VERSION="$CURRENT_VERSION"
  install -m 0755 "$RUNTIME_BINARY" "$RESTORE_BINARY"
  install -m 0755 -o dengdeng -g dengdeng "$RELEASE_DIRECTORY/dengdeng-$TARGET_COMMIT" "$RUNTIME_BINARY.new"
  mv -f "$RUNTIME_BINARY.new" "$RUNTIME_BINARY"
  printf '%s\n' "$TARGET_COMMIT" > "$CURRENT_COMMIT_FILE"
  printf '%s\n' "$TARGET_VERSION" > "$CURRENT_VERSION_FILE"
  SWITCHED="true"
  systemctl restart "$SERVICE"

  set_stage "health_check" "正在检查新版本"
  healthy
  install -m 0755 "$RESTORE_BINARY" "$PREVIOUS_BINARY"
  printf '%s\n' "$RESTORE_COMMIT" > "$PREVIOUS_COMMIT_FILE"
  CURRENT_COMMIT="$TARGET_COMMIT"
  CURRENT_VERSION="$TARGET_VERSION"
  PREVIOUS_COMMIT="$RESTORE_COMMIT"
  UPDATE_AVAILABLE="false"
  sync_updater_components
  rm -f "$RESTORE_BINARY"
  SWITCHED="false"
  STATUS="succeeded"
  STAGE="completed"
  MESSAGE="新版本已上线并通过健康检查"
  FINISHED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  write_state
}

rollback_release() {
  [[ -x "$PREVIOUS_BINARY" && -n "$PREVIOUS_COMMIT" ]]
  set_stage "switching" "正在恢复上一版本"
  RESTORE_BINARY="$RELEASE_DIRECTORY/dengdeng.rollback-current"
  RESTORE_COMMIT="$CURRENT_COMMIT"
  RESTORE_VERSION="$CURRENT_VERSION"
  install -m 0755 "$RUNTIME_BINARY" "$RESTORE_BINARY"
  local previous_version
  previous_version="$(git -C "$SOURCE_DIRECTORY" describe --tags --always "$PREVIOUS_COMMIT" 2>/dev/null || printf '%s' "${PREVIOUS_COMMIT:0:12}")"
  install -m 0755 -o dengdeng -g dengdeng "$PREVIOUS_BINARY" "$RUNTIME_BINARY.new"
  mv -f "$RUNTIME_BINARY.new" "$RUNTIME_BINARY"
  printf '%s\n' "$PREVIOUS_COMMIT" > "$CURRENT_COMMIT_FILE"
  printf '%s\n' "$previous_version" > "$CURRENT_VERSION_FILE"
  SWITCHED="true"
  systemctl restart "$SERVICE"

  set_stage "health_check" "正在检查回滚版本"
  healthy
  install -m 0755 "$RESTORE_BINARY" "$PREVIOUS_BINARY"
  printf '%s\n' "$RESTORE_COMMIT" > "$PREVIOUS_COMMIT_FILE"
  rm -f "$RESTORE_BINARY"
  TARGET_COMMIT="$PREVIOUS_COMMIT"
  CURRENT_COMMIT="$TARGET_COMMIT"
  CURRENT_VERSION="$previous_version"
  PREVIOUS_COMMIT="$RESTORE_COMMIT"
  UPDATE_AVAILABLE="true"
  SWITCHED="false"
  STATUS="succeeded"
  STAGE="completed"
  MESSAGE="已恢复上一版本并通过健康检查"
  FINISHED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  write_state
}

[[ -r "$REQUEST_FILE" ]]
ACTION="$(json_field action)"
REQUESTED_BY="$(json_field requested_by)"
REQUESTED_AT="$(json_field requested_at)"
[[ "$ACTION" == "check" || "$ACTION" == "apply" || "$ACTION" == "rollback" ]]
load_markers
write_state

case "$ACTION" in
  check)
    prepare_repository
    STATUS="succeeded"
    STAGE="ready"
    if [[ "$UPDATE_AVAILABLE" == "true" ]]; then MESSAGE="发现仓库新版本"; else MESSAGE="当前已经是仓库最新版本"; fi
    FINISHED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    write_state
    ;;
  apply)
    prepare_repository
    apply_release
    ;;
  rollback)
    rollback_release
    ;;
esac
