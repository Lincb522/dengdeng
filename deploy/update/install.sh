#!/usr/bin/env bash
set -Eeuo pipefail

[[ "${EUID:-$(id -u)}" -eq 0 ]] || { echo "请使用 root 运行安装脚本" >&2; exit 1; }
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

if command -v dnf >/dev/null 2>&1; then
  dnf install -y git golang nodejs nodejs-npm python3 polkit
elif command -v apt-get >/dev/null 2>&1; then
  apt-get update
  DEBIAN_FRONTEND=noninteractive apt-get install -y git golang-go nodejs npm python3 policykit-1
else
  echo "不支持的包管理器，请先安装 Git、Go 1.25+、Node.js 22+、npm 与 Python 3" >&2
  exit 1
fi

version_at_least() {
  local actual="$1" required="$2"
  [[ "$(printf '%s\n%s\n' "$required" "$actual" | sort -V | head -n 1)" == "$required" ]]
}

NODE_VERSION="$(node -p 'process.versions.node')"
GO_VERSION="$(go env GOVERSION)"
GO_VERSION="${GO_VERSION#go}"
version_at_least "$NODE_VERSION" "22.0.0" || { echo "Node.js $NODE_VERSION 过旧，需要 22.0.0 或更高版本" >&2; exit 1; }
version_at_least "$GO_VERSION" "1.25.6" || { echo "Go $GO_VERSION 过旧，需要 1.25.6 或更高版本" >&2; exit 1; }

npm install --global pnpm@11.14.0
install -d -m 0750 /opt/dengdeng/source /opt/dengdeng/releases /opt/dengdeng/.update-home
install -d -m 0750 -o dengdeng -g dengdeng /var/lib/dengdeng/update
install -d -m 0700 /etc/dengdeng
install -m 0755 "$ROOT/deploy/update/dengdeng-update.sh" /usr/local/sbin/dengdeng-update
install -m 0644 "$ROOT/deploy/systemd/dengdeng-updater.service" /etc/systemd/system/dengdeng-updater.service
install -m 0644 "$ROOT/deploy/polkit/49-dengdeng-updater.rules" /etc/polkit-1/rules.d/49-dengdeng-updater.rules

if [[ ! -f /etc/dengdeng/update.conf ]]; then
  install -m 0600 "$ROOT/deploy/update/update.conf.example" /etc/dengdeng/update.conf
fi

if [[ ! -d /opt/dengdeng/source/.git ]]; then
  git clone --filter=blob:none --branch main https://github.com/Lincb522/dengdeng.git /opt/dengdeng/source
else
  git -C /opt/dengdeng/source fetch --prune --tags origin
fi

if ! grep -q '^UPDATE_ENABLED=' /etc/dengdeng/dengdeng.env; then
  printf '\nUPDATE_ENABLED=true\nUPDATE_REPOSITORY=https://github.com/Lincb522/dengdeng.git\nUPDATE_BRANCH=main\nUPDATE_STATE_DIRECTORY=/var/lib/dengdeng/update\n' >> /etc/dengdeng/dengdeng.env
else
  sed -i 's/^UPDATE_ENABLED=.*/UPDATE_ENABLED=true/' /etc/dengdeng/dengdeng.env
fi

systemctl daemon-reload
systemctl restart polkit.service
systemctl restart dengdeng.service
HEALTH_JSON=""
for _ in $(seq 1 30); do
  if HEALTH_JSON="$(curl --fail --silent --max-time 3 http://127.0.0.1:9100/health)"; then
    break
  fi
  sleep 1
done
[[ -n "$HEALTH_JSON" ]] || { echo "主服务健康检查失败，请检查 dengdeng.service" >&2; exit 1; }

CURRENT_COMMIT="$(python3 -c 'import json,sys; print(json.load(sys.stdin).get("commit", ""))' <<<"$HEALTH_JSON")"
CURRENT_VERSION="$(python3 -c 'import json,sys; print(json.load(sys.stdin).get("version", ""))' <<<"$HEALTH_JSON")"
if [[ -n "$CURRENT_COMMIT" && "$CURRENT_COMMIT" != "unknown" ]]; then
  printf '%s\n' "$CURRENT_COMMIT" > /opt/dengdeng/releases/CURRENT_COMMIT
fi
if [[ -n "$CURRENT_VERSION" && "$CURRENT_VERSION" != "dev" ]]; then
  printf '%s\n' "$CURRENT_VERSION" > /opt/dengdeng/releases/CURRENT_VERSION
fi
echo "服务器仓库更新已安装。请登录管理端执行一次“检查更新”。"
