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
echo "服务器仓库更新已安装。请登录管理端执行一次“检查更新”。"
