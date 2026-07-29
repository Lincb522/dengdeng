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
CHANGELOG_FILE="$STATE_DIRECTORY/changelog.json"
HISTORY_FILE="$STATE_DIRECTORY/history.json"
LOCK_FILE="$STATE_DIRECTORY/update.lock"
CURRENT_COMMIT_FILE="$RELEASE_DIRECTORY/CURRENT_COMMIT"
CURRENT_VERSION_FILE="$RELEASE_DIRECTORY/CURRENT_VERSION"
PREVIOUS_COMMIT_FILE="$RELEASE_DIRECTORY/PREVIOUS_COMMIT"
PREVIOUS_BINARY="$RELEASE_DIRECTORY/dengdeng.previous"
HOME_DIRECTORY="/opt/dengdeng/.update-home"
BUILD_DIRECTORY="$SOURCE_DIRECTORY/.build"

for path in "$SOURCE_DIRECTORY" "$RELEASE_DIRECTORY" "$RUNTIME_BINARY" "$STATE_DIRECTORY"; do
  [[ "$path" == /* ]] || { echo "update paths must be absolute" >&2; exit 2; }
done
[[ "$BRANCH" =~ ^[A-Za-z0-9._/-]+$ && "$BRANCH" != -* && "$BRANCH" != *".."* ]] || { echo "invalid branch" >&2; exit 2; }
[[ "$SERVICE" =~ ^[A-Za-z0-9_.@-]+\.service$ ]] || { echo "invalid service" >&2; exit 2; }
[[ "$BUILD_JOBS" =~ ^[1-9][0-9]?$ ]] || BUILD_JOBS=2

install -d -m 0750 -o dengdeng -g dengdeng "$STATE_DIRECTORY" "$SOURCE_DIRECTORY" "$HOME_DIRECTORY"
install -d -m 0750 -o root -g dengdeng "$RELEASE_DIRECTORY"
exec 9>"$LOCK_FILE"
flock -n 9 || exit 0

builder() {
  runuser -u dengdeng -- env \
    HOME="$HOME_DIRECTORY" \
    PNPM_HOME="$HOME_DIRECTORY/pnpm" \
    GOPROXY="$GOPROXY" \
    PATH="$HOME_DIRECTORY/pnpm:/usr/local/bin:/usr/bin:/bin" \
    "$@"
}

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
  export DD_UPDATE_CHANGELOG_FILE="$CHANGELOG_FILE"
  python3 <<'PY'
import json, os, pathlib
path = pathlib.Path(os.environ["DD_UPDATE_STATUS_FILE"])
try:
    changes = json.loads(pathlib.Path(os.environ["DD_UPDATE_CHANGELOG_FILE"]).read_text(encoding="utf-8"))
    if not isinstance(changes, list):
        changes = []
except (OSError, ValueError, TypeError):
    changes = []
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
    "changes": changes,
}
temporary = path.with_suffix(".tmp")
temporary.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
temporary.chmod(0o640)
os.replace(temporary, path)
PY
  chown dengdeng:dengdeng "$STATUS_FILE"
}

write_changelog() {
  export DD_UPDATE_SOURCE="$SOURCE_DIRECTORY" DD_UPDATE_CURRENT_COMMIT="$CURRENT_COMMIT"
  export DD_UPDATE_TARGET_COMMIT="$TARGET_COMMIT" DD_UPDATE_CHANGELOG_FILE="$CHANGELOG_FILE"
  python3 <<'PY'
import json, os, pathlib, subprocess

source = os.environ["DD_UPDATE_SOURCE"]
current = os.environ["DD_UPDATE_CURRENT_COMMIT"]
target = os.environ["DD_UPDATE_TARGET_COMMIT"]
path = pathlib.Path(os.environ["DD_UPDATE_CHANGELOG_FILE"])
changes = []

if target and target != current:
    revision = target
    if current:
        exists = subprocess.run(
            ["git", "-C", source, "cat-file", "-e", current + "^{commit}"],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        ).returncode == 0
        ancestor = exists and subprocess.run(
            ["git", "-C", source, "merge-base", "--is-ancestor", current, target],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        ).returncode == 0
        if ancestor:
            revision = current + ".." + target
    # The updater writes the release log before any build starts. Commit-body
    # bullet points become structured details in the admin update page, so a
    # release keeps its full explanation instead of only a terse subject line.
    result = subprocess.run(
        ["git", "-C", source, "log", "--max-count=30", "--format=%H%x1f%s%x1f%cI%x1f%b%x1e", revision],
        check=False, capture_output=True, text=True,
    )
    if result.returncode == 0:
        for record in result.stdout.split("\x1e"):
            fields = record.strip("\r\n").split("\x1f", 3)
            if len(fields) != 4:
                continue
            details = []
            for raw_line in fields[3].splitlines():
                line = raw_line.strip()
                if not line:
                    continue
                line = line.removeprefix("- ").removeprefix("* ").strip()
                if line and line not in details:
                    details.append(line)
                if len(details) >= 12:
                    break
            changes.append({
                "commit": fields[0],
                "title": fields[1],
                "committed_at": fields[2],
                "details": details,
            })

temporary = path.with_suffix(".tmp")
temporary.write_text(json.dumps(changes, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
temporary.chmod(0o640)
os.replace(temporary, path)
PY
  chown dengdeng:dengdeng "$CHANGELOG_FILE"
}

append_release_history() {
  export DD_UPDATE_HISTORY_FILE="$HISTORY_FILE" DD_UPDATE_CHANGELOG_FILE="$CHANGELOG_FILE"
  export DD_UPDATE_CURRENT_VERSION="$CURRENT_VERSION" DD_UPDATE_CURRENT_COMMIT="$CURRENT_COMMIT"
  export DD_UPDATE_PREVIOUS_COMMIT="$PREVIOUS_COMMIT" DD_UPDATE_ACTION="$ACTION" DD_UPDATE_MESSAGE="$MESSAGE"
  export DD_UPDATE_REQUESTED_BY="$REQUESTED_BY" DD_UPDATE_FINISHED_AT="$FINISHED_AT"
  python3 <<'PY'
import json, os, pathlib

history_path = pathlib.Path(os.environ["DD_UPDATE_HISTORY_FILE"])
changelog_path = pathlib.Path(os.environ["DD_UPDATE_CHANGELOG_FILE"])
try:
    history = json.loads(history_path.read_text(encoding="utf-8"))
    if not isinstance(history, list):
        history = []
except (OSError, ValueError, TypeError):
    history = []
try:
    changes = json.loads(changelog_path.read_text(encoding="utf-8"))
    if not isinstance(changes, list):
        changes = []
except (OSError, ValueError, TypeError):
    changes = []

release = {
    "version": os.environ["DD_UPDATE_CURRENT_VERSION"],
    "commit": os.environ["DD_UPDATE_CURRENT_COMMIT"],
    "previous_commit": os.environ["DD_UPDATE_PREVIOUS_COMMIT"],
    "action": os.environ["DD_UPDATE_ACTION"],
    "message": os.environ["DD_UPDATE_MESSAGE"],
    "requested_by": os.environ["DD_UPDATE_REQUESTED_BY"],
    "finished_at": os.environ["DD_UPDATE_FINISHED_AT"],
    "changes": changes,
}
history = [item for item in history if not (
    isinstance(item, dict)
    and item.get("commit") == release["commit"]
    and item.get("finished_at") == release["finished_at"]
)]
history.insert(0, release)
history = history[:50]
temporary = history_path.with_suffix(".tmp")
temporary.write_text(json.dumps(history, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
temporary.chmod(0o640)
os.replace(temporary, history_path)
PY
  chown dengdeng:dengdeng "$HISTORY_FILE"
}

seed_release_history() {
  [[ -r "$STATUS_FILE" ]] || return 0
  export DD_UPDATE_STATUS_FILE="$STATUS_FILE" DD_UPDATE_HISTORY_FILE="$HISTORY_FILE"
  python3 <<'PY'
import json, os, pathlib

status_path = pathlib.Path(os.environ["DD_UPDATE_STATUS_FILE"])
history_path = pathlib.Path(os.environ["DD_UPDATE_HISTORY_FILE"])
try:
    status = json.loads(status_path.read_text(encoding="utf-8"))
except (OSError, ValueError, TypeError):
    raise SystemExit(0)
if status.get("status") != "succeeded" or status.get("action") not in ("apply", "rollback"):
    raise SystemExit(0)
if not status.get("current_commit") or not status.get("finished_at"):
    raise SystemExit(0)
try:
    history = json.loads(history_path.read_text(encoding="utf-8"))
    if not isinstance(history, list):
        history = []
except (OSError, ValueError, TypeError):
    history = []
release = {
    "version": status.get("current_version", ""),
    "commit": status["current_commit"],
    "previous_commit": status.get("previous_commit", ""),
    "action": status["action"],
    "message": status.get("message", ""),
    "requested_by": status.get("requested_by", ""),
    "finished_at": status["finished_at"],
    "changes": status.get("changes") if isinstance(status.get("changes"), list) else [],
}
if not any(isinstance(item, dict) and item.get("commit") == release["commit"] and item.get("finished_at") == release["finished_at"] for item in history):
    history.insert(0, release)
history = history[:50]
temporary = history_path.with_suffix(".tmp")
temporary.write_text(json.dumps(history, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
temporary.chmod(0o640)
os.replace(temporary, history_path)
PY
  [[ ! -e "$HISTORY_FILE" ]] || chown dengdeng:dengdeng "$HISTORY_FILE"
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
  builder git -C "$SOURCE_DIRECTORY" restore -- backend/internal/web/dist/index.html 2>/dev/null || true
  restore_after_failure
  exit "$code"
}
trap on_error ERR

prepare_repository() {
  set_stage "fetching" "正在同步远程仓库"
  if [[ ! -d "$SOURCE_DIRECTORY/.git" ]]; then
    rm -rf "$SOURCE_DIRECTORY"
    install -d -m 0750 -o dengdeng -g dengdeng "$SOURCE_DIRECTORY"
    builder git clone --filter=blob:none --no-tags --branch "$BRANCH" "$REPOSITORY" "$SOURCE_DIRECTORY"
  fi
  builder git -C "$SOURCE_DIRECTORY" remote set-url origin "$REPOSITORY"
  builder git -C "$SOURCE_DIRECTORY" fetch --prune --tags origin "+refs/heads/$BRANCH:refs/remotes/origin/$BRANCH"
  TARGET_COMMIT="$(builder git -C "$SOURCE_DIRECTORY" rev-parse --verify "origin/$BRANCH^{commit}")"
  [[ "$TARGET_COMMIT" =~ ^[0-9a-f]{40}$ ]]
  if [[ -n "$CURRENT_COMMIT" && "$CURRENT_COMMIT" == "$TARGET_COMMIT" ]]; then
    UPDATE_AVAILABLE="false"
  else
    UPDATE_AVAILABLE="true"
  fi
  write_changelog
}

build_release() {
  set_stage "building_frontend" "正在构建管理端"
  builder git -C "$SOURCE_DIRECTORY" checkout -f -B "$BRANCH" "origin/$BRANCH"
  builder git -C "$SOURCE_DIRECTORY" reset --hard "$TARGET_COMMIT"
  builder git -C "$SOURCE_DIRECTORY" clean -ffd
  builder bash -c 'cd "$1" && pnpm install --frozen-lockfile --prefer-offline && pnpm build' _ "$SOURCE_DIRECTORY/frontend"

  set_stage "building_backend" "正在构建服务端"
  local release build_output version_name build_time ldflags
  release="$RELEASE_DIRECTORY/dengdeng-$TARGET_COMMIT"
  build_output="$BUILD_DIRECTORY/dengdeng-$TARGET_COMMIT"
  version_name="$(builder git -C "$SOURCE_DIRECTORY" describe --tags --always "$TARGET_COMMIT")"
  build_time="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  ldflags="-s -w -X dengdeng/internal/version.Version=$version_name -X dengdeng/internal/version.Commit=$TARGET_COMMIT -X dengdeng/internal/version.BuildTime=$build_time"
  builder install -d -m 0750 "$BUILD_DIRECTORY"
  builder bash -c 'cd "$1" && GOMAXPROCS="$2" CGO_ENABLED=0 go build -p "$2" -trimpath -ldflags "$3" -o "$4" ./cmd/server' _ "$SOURCE_DIRECTORY/backend" "$BUILD_JOBS" "$ldflags" "$build_output"
  builder git -C "$SOURCE_DIRECTORY" restore -- backend/internal/web/dist/index.html
  install -m 0755 -o root -g dengdeng "$build_output" "$release"
  builder rm -f "$build_output"
  TARGET_VERSION="$version_name"
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
  rm -f "$RESTORE_BINARY"
  SWITCHED="false"
  STATUS="succeeded"
  STAGE="completed"
  MESSAGE="新版本已上线并通过健康检查"
  FINISHED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  write_state
  append_release_history
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
  append_release_history
}

[[ -r "$REQUEST_FILE" ]]
ACTION="$(json_field action)"
REQUESTED_BY="$(json_field requested_by)"
REQUESTED_AT="$(json_field requested_at)"
[[ "$ACTION" == "check" || "$ACTION" == "apply" || "$ACTION" == "rollback" ]]
load_markers
seed_release_history
printf '[]\n' > "$CHANGELOG_FILE"
chmod 0640 "$CHANGELOG_FILE"
chown dengdeng:dengdeng "$CHANGELOG_FILE"
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
