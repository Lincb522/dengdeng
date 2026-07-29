# 部署手册

下面按两种常见方式写：Docker Compose 适合先上线和单机维护；二进制 + systemd 更适合已有服务器规范的环境。无论哪种方式，服务本身只监听 `127.0.0.1:9100`，由 Nginx 或其他反向代理公开 80/443。

## 方式一：Docker Compose

```bash
git clone git@github.com:YOUR_ACCOUNT/dengdeng.git
cd dengdeng/deploy
cp .env.example .env
chmod 600 .env
# 编辑 .env，至少填写 JWT_SECRET、ENCRYPTION_KEY 和管理员账号信息
docker compose up -d --build
docker compose ps
curl -fsS http://127.0.0.1:9100/health
```

Docker 数据默认保存在 `deploy/data/`。它不在 Git 中；请单独备份，并至少做一次恢复演练。

## 方式二：单二进制 + systemd

构建 Linux 二进制：

```bash
cd frontend && pnpm install && pnpm build
cd ../backend
COMMIT="$(git rev-parse HEAD)"
VERSION="$(git describe --tags --always)"
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath \
  -ldflags="-s -w -X dengdeng/internal/version.Version=$VERSION -X dengdeng/internal/version.Commit=$COMMIT -X dengdeng/internal/version.BuildTime=$BUILD_TIME" \
  -o dengdeng ./cmd/server
```

在服务器上创建运行账户和目录：

```bash
sudo useradd --system --home /nonexistent --shell /usr/sbin/nologin dengdeng
sudo install -d -o dengdeng -g dengdeng /opt/dengdeng /var/lib/dengdeng /etc/dengdeng
sudo install -m 0755 dengdeng /opt/dengdeng/dengdeng
sudo install -m 0600 /dev/null /etc/dengdeng/dengdeng.env
```

`/etc/dengdeng/dengdeng.env` 至少应包含：

```dotenv
SERVER_HOST=127.0.0.1
SERVER_PORT=9100
DATABASE_PATH=/var/lib/dengdeng/dengdeng.db
JWT_SECRET=replace-with-a-random-value
ENCRYPTION_KEY=replace-with-a-separate-random-value
ADMIN_EMAIL=admin@example.com
ADMIN_PASSWORD=replace-before-first-start
SITE_PUBLIC_URL=https://your-domain.example
```

安装仓库内的 [systemd 单元](../deploy/systemd/dengdeng.service) 后启动：

```bash
sudo install -m 0644 deploy/systemd/dengdeng.service /etc/systemd/system/dengdeng.service
sudo systemctl daemon-reload
sudo systemctl enable --now dengdeng
sudo systemctl status dengdeng
```

## Nginx 与 HTTPS

仓库的 `deploy/nginx/` 有可直接改域名的示例。它关闭了代理缓冲，并把请求体限制设置为 `65m`，避免较长的 Responses 请求和图像上传在 Nginx 层被截断。

```bash
sudo nginx -t
sudo systemctl reload nginx
curl -fsS https://your-domain.example/health
```

支付和 OAuth 回调依赖 `SITE_PUBLIC_URL`，其域名、HTTPS 证书和上游平台登记的回调地址必须完全一致。

## 自动备份与保留策略

SQLite 部署默认每 24 小时创建一次一致性快照，保留 30 天且最多保留 30 份。可以在「系统维护 → 数据库备份」随时启停或调整间隔、天数和份数；设置保存在数据库中，服务重启和版本更新不会丢失。

保留策略仅清理创建者标记为 `system:auto` 的系统自动备份。管理员手动创建的备份不会自动删除，也不会因为超过保留天数而消失。生产环境建议把备份目录放在独立持久化磁盘，并定期复制到异机或对象存储：

```dotenv
BACKUP_DIRECTORY=/var/lib/dengdeng/backups
BACKUP_AUTO_ENABLED=true
BACKUP_INTERVAL_HOURS=24
BACKUP_RETENTION_DAYS=30
BACKUP_RETENTION_COUNT=30
```

“立即清理”只立即执行同一套自动备份保留规则，不会清空数据库、用量日志或手动备份。

### GitHub 加密异地备份

不要把 SQLite 明文、环境变量或恢复密钥提交到 GitHub。仓库提供的 `deploy/backup/dengdeng-github-backup.sh` 会先用 `VACUUM INTO` 创建一致性快照，通过 `PRAGMA integrity_check` 后进行压缩和 GPG AES-256 加密，再把密文推送到独立私有仓库。默认保留最近 7 份完整数据库快照和 2 份脱敏全栈快照，并把大文件切成 90 MiB 分片。

全栈快照包含当前提交的源码、运行二进制、Nginx/systemd 配置、运行环境文件和一份业务数据库副本。生成副本时会停用在线支付和推广提现，删除支付渠道、商户密钥、用户提现 OpenID，并清空订单渠道快照和外部支付流水绑定。TLS 私钥、GitHub deploy key 和备份解密口令不会进入快照。其他业务数据与加密上游凭据仍会保留，因此全栈文件即使加密后也必须只放在私有仓库。

为备份仓库创建一把只用于该仓库的可写 deploy key。服务器私钥、恢复口令和主应用密钥相互独立：

```bash
sudo install -d -m 0700 -o dengdeng -g dengdeng /var/lib/dengdeng/github-backup
sudo -u dengdeng ssh-keygen -t ed25519 -N '' \
  -f /var/lib/dengdeng/github-backup/id_ed25519 \
  -C 'dengdeng-github-backup'
sudo install -m 0644 deploy/systemd/dengdeng-github-backup.service /etc/systemd/system/
sudo install -m 0644 deploy/systemd/dengdeng-github-backup.timer /etc/systemd/system/
sudo install -m 0755 deploy/backup/dengdeng-github-backup.sh /usr/local/sbin/
sudo install -m 0755 deploy/backup/dengdeng-github-restore.sh /usr/local/sbin/
sudo install -m 0755 deploy/backup/dengdeng-fullstack-restore.sh /usr/local/sbin/
sudo install -m 0640 -o root -g dengdeng \
  deploy/backup/github-backup.conf.example /etc/dengdeng/github-backup.conf
sudo sh -c 'umask 027; openssl rand -base64 48 > /etc/dengdeng/github-backup.pass'
sudo chown root:dengdeng /etc/dengdeng/github-backup.pass
```

把 GitHub 官方公布的 `github.com` Ed25519 主机公钥写入 `/var/lib/dengdeng/github-backup/known_hosts`，将生成的公钥注册为备份仓库的可写 deploy key，再修改 `/etc/dengdeng/github-backup.conf` 中的仓库地址。首次执行应同时验证上传与恢复：

```bash
sudo systemctl daemon-reload
sudo systemctl start dengdeng-github-backup.service
sudo journalctl -u dengdeng-github-backup.service --no-pager -n 50

git clone git@github.com:YOUR_ACCOUNT/dengdeng-backups.git /tmp/dengdeng-backups
sudo DENGDENG_BACKUP_PASSPHRASE_FILE=/etc/dengdeng/github-backup.pass \
  /usr/local/sbin/dengdeng-github-restore \
  /tmp/dengdeng-backups latest /tmp/dengdeng-restored.db
sudo sqlite3 /tmp/dengdeng-restored.db 'PRAGMA integrity_check;'

sudo DENGDENG_BACKUP_PASSPHRASE_FILE=/etc/dengdeng/github-backup.pass \
  /usr/local/sbin/dengdeng-fullstack-restore \
  /tmp/dengdeng-backups latest /tmp/dengdeng-fullstack-restored
sudo sqlite3 /tmp/dengdeng-fullstack-restored/database/dengdeng.sanitized.db \
  'PRAGMA integrity_check;'

sudo systemctl enable --now dengdeng-github-backup.timer
systemctl list-timers dengdeng-github-backup.timer
```

恢复脚本永远只写入新文件，不会自动覆盖生产数据库。恢复口令必须另存到服务器之外且权限设为 `0600`；丢失口令后 GitHub 中的密文无法恢复。

## 服务器直连仓库与版本更新

二进制 + systemd 部署可以安装独立更新器，让管理员在「系统维护 → 版本更新」检查 `main`、执行更新或恢复上一版本。源码固定放在 `/opt/dengdeng/source`，运行二进制仍是 `/opt/dengdeng/dengdeng`；前端和后端全部构建成功前不会触碰线上进程，构建结束后也会恢复仓库中的前端占位文件以保持工作区干净。

首次安装需要 root 权限：

```bash
cd /path/to/dengdeng
sudo bash deploy/update/install.sh
```

安装脚本会完成以下一次性配置：

- 安装并校验 Git、Go 1.25.6+、Node.js 22+、pnpm 与 Python 3，并克隆受信任仓库；
- 安装 `dengdeng-updater.service` 和 `/usr/local/sbin/dengdeng-update`；
- 写入最小化 Polkit 规则：应用账户只能启动固定 updater 单元，不能传入命令或仓库地址；
- 开启 `UPDATE_ENABLED`，重启主服务以显示管理端入口。

仓库和分支由 root 独占的 `/etc/dengdeng/update.conf` 决定。调整后无需把 GitHub 凭据交给网页；私有仓库请只给 root 的 Git 客户端配置只读 deploy key。日常流程如下：

`GOPROXY` 也在该文件中显式配置，默认使用 `https://proxy.golang.org,direct`；网络环境有要求时可由 root 改为可信的内部模块代理。

1. 「检查更新」只执行 `git fetch` 并比较提交，不重启服务。
2. 「更新到最新版本」先通过应用创建一致性 SQLite 快照，再在独立 systemd 任务中构建。
3. 构建成功后原子替换二进制，短暂重启并连续检查 `/health`。
4. 健康检查失败会自动恢复旧二进制；「恢复上一版本」也会在切换前创建数据库快照。

检查完成后，管理端会列出当前提交到目标提交之间最多 30 条更新日志，包括标题、短提交号和提交时间。更新成功后仍保留本次日志，便于核对实际上线内容。

更新成功后，执行器会从同一个已验证提交同步自身脚本、systemd 单元和 Polkit 规则；root 独占的 `/etc/dengdeng/update.conf` 不会被网页或自动更新覆盖。

当前部署是单实例监听 `127.0.0.1:9100`，版本切换期间通常会有数秒连接重试窗口。它是受控热更新，不承诺多实例蓝绿架构才具备的绝对零停机。更新状态保存在 `/var/lib/dengdeng/update/status.json`，详细构建输出使用：

```bash
sudo journalctl -u dengdeng-updater.service -f
```

若不启用网页更新，仍可使用手工流程：先创建数据库快照，构建并上传临时二进制，校验后原子替换，重启并检查 `/health`；异常时替换回上一已验证二进制。

生产 `.env`、数据库、TLS 私钥、支付密钥和上游凭据都只留在服务器或 Secret 管理服务里；不要复制进仓库、截图或构建日志。
