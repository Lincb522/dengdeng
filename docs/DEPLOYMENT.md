# 部署

生产环境建议使用单个网关实例、独立 HTTPS 反向代理和持久化数据库。并发槽、等待队列和部分调度状态当前在进程内；没有共享协调层时不要通过复制实例做负载均衡。

## 运行要求

### 容器部署

- Docker Engine 24 或更高
- Docker Compose v2
- amd64 或 arm64 Linux
- 一个可写的持久化目录
- 公网使用时需要域名和 HTTPS 反向代理

### 源码构建

- Go 1.26.5
- Node.js 26
- pnpm 11.14.0
- Git

版本以 `backend/go.mod`、`frontend/package.json`、`Dockerfile` 和 `.github/workflows/quality.yml` 为准。

## 配置加载

服务默认读取当前工作目录的 `config.yaml`。也可以显式指定：

```bash
./dengdeng -config /etc/dengdeng/config.yaml
```

优先级从低到高：

1. 代码默认值；
2. YAML；
3. 环境变量；
4. 管理端保存到数据库的运行设置，仅覆盖其所属领域。

`JWT_SECRET` 是唯一启动时强制要求的值。生产环境还必须设置独立的 `ENCRYPTION_KEY`。不设置时程序会从 `JWT_SECRET` 派生字段加密密钥并写入警告日志，但这不适合作为长期生产配置。

### 启动环境变量

| 领域 | 环境变量 |
| --- | --- |
| HTTP | `SERVER_HOST`、`SERVER_PORT`、`SERVER_MODE` |
| 真实 IP | `SERVER_TRUSTED_PROXIES`、`SERVER_FORWARDED_CLIENT_IP_HEADERS` |
| SQLite | `DATABASE_DRIVER=sqlite`、`DATABASE_PATH` |
| PostgreSQL | `DATABASE_DRIVER=postgres`、`DATABASE_HOST`、`DATABASE_PORT`、`DATABASE_USER`、`DATABASE_PASSWORD`、`DATABASE_DBNAME`、`DATABASE_SSLMODE` |
| 会话与加密 | `JWT_SECRET`、`JWT_EXPIRE_HOUR`、`ENCRYPTION_KEY` |
| 初始管理员 | `ADMIN_EMAIL`、`ADMIN_PASSWORD` |
| 站点 | `SITE_NAME`、`SITE_PUBLIC_URL`、`SITE_ALLOW_REGISTER` |
| SMTP | `SMTP_HOST`、`SMTP_PORT`、`SMTP_SECURE`、`SMTP_USER`、`SMTP_PASS`、`SMTP_FROM_NAME`、`SMTP_FROM` |
| 自动备份 | `BACKUP_DIRECTORY`、`BACKUP_AUTO_ENABLED`、`BACKUP_INTERVAL_HOURS`、`BACKUP_RETENTION_DAYS`、`BACKUP_RETENTION_COUNT` |
| 更新器入口 | `UPDATE_ENABLED`、`UPDATE_REPOSITORY`、`UPDATE_BRANCH`、`UPDATE_STATE_DIRECTORY` |
| 全局出站 | `PROXY_URL`、`NO_PROXY` |
| 上游 OAuth | `OAUTH_OPENAI_*`、`OAUTH_ANTHROPIC_*`、`OAUTH_GEMINI_*`、`XAI_OAUTH_*` |

常用字段可从 [配置模板](../deploy/config.example.yaml) 和 [容器环境模板](../deploy/.env.example) 复制。真实 `.env`、`config.yaml` 和密钥文件已被 `.gitignore` 排除，不应提交到仓库。

`SITE_PUBLIC_URL` 必须是浏览器和支付平台实际访问的 HTTPS Origin。用户 OAuth、支付回调、支付完成跳转和推广提现页面会使用它，值错误会导致回调不匹配或跳转到错误域名。

## Docker Compose

```bash
git clone https://github.com/Lincb522/dengdeng.git
cd dengdeng/deploy
cp .env.example .env
```

生成两组不同的随机值：

```bash
openssl rand -hex 32
openssl rand -hex 32
```

写入 `.env`：

```dotenv
JWT_SECRET=replace-with-random-value
ENCRYPTION_KEY=replace-with-another-random-value
ADMIN_EMAIL=admin@example.com
ADMIN_PASSWORD=replace-with-a-strong-password
SITE_PUBLIC_URL=https://relay.example.com
BIND_HOST=127.0.0.1
SERVER_PORT=9100
```

启动：

```bash
docker compose pull
docker compose up -d
docker compose ps
curl -fsS http://127.0.0.1:9100/health
```

默认镜像是 `ghcr.io/lincb522/dengdeng:latest`，支持 amd64 与 arm64。SQLite 数据写入 `deploy/data/`。升级前先备份该目录：

```bash
docker compose pull
docker compose up -d
docker compose ps
docker compose logs --tail=100 dengdeng
```

不要使用 `docker compose down -v` 删除持久卷；当前 Compose 使用 bind mount，但生产操作仍应把删除卷视为破坏性命令。

Compose 只会向容器传入 `deploy/docker-compose.yml` 的 `environment` 段中列出的变量。需要使用 SMTP、代理或额外 OAuth 启动配置时，应在该段显式加入对应变量，或挂载完整 YAML 并通过 `-config` 指定。

### PostgreSQL

取消 `deploy/docker-compose.yml` 中 PostgreSQL 服务和对应环境变量的注释，设置独立强密码，并把应用改为 `DATABASE_DRIVER=postgres`。已有 SQLite 数据不会自动导入 PostgreSQL。

使用 PostgreSQL 后：

- 应用内置“数据库备份”页面不可用于 PostgreSQL；
- 备份、恢复、保留周期和时间点恢复交给 PostgreSQL 工具或托管平台；
- 数据库凭据放在 Secret 管理或受限环境文件中。

## systemd 单二进制

### 构建

```bash
git clone https://github.com/Lincb522/dengdeng.git
cd dengdeng

corepack enable
cd frontend
pnpm install --frozen-lockfile
pnpm build

cd ../backend
go test ./...
go build -trimpath -o ../dengdeng ./cmd/server
```

### 安装

```bash
sudo useradd --system --home /nonexistent --shell /usr/sbin/nologin dengdeng
sudo install -d -m 0750 -o dengdeng -g dengdeng /opt/dengdeng /var/lib/dengdeng
sudo install -d -m 0700 /etc/dengdeng
sudo install -m 0755 dengdeng /opt/dengdeng/dengdeng
sudo install -m 0600 /dev/null /etc/dengdeng/dengdeng.env
```

写入 `/etc/dengdeng/dengdeng.env`：

```dotenv
SERVER_HOST=127.0.0.1
SERVER_PORT=9100
DATABASE_DRIVER=sqlite
DATABASE_PATH=/var/lib/dengdeng/dengdeng.db
BACKUP_DIRECTORY=/var/lib/dengdeng/backups
JWT_SECRET=replace-with-random-value
ENCRYPTION_KEY=replace-with-another-random-value
ADMIN_EMAIL=admin@example.com
ADMIN_PASSWORD=replace-with-a-strong-password
SITE_PUBLIC_URL=https://relay.example.com
```

安装服务：

```bash
sudo install -m 0644 deploy/systemd/dengdeng.service /etc/systemd/system/dengdeng.service
sudo systemctl daemon-reload
sudo systemctl enable --now dengdeng.service
sudo systemctl status dengdeng.service
sudo journalctl -u dengdeng.service -n 100 --no-pager
```

仓库的 systemd 单元以 `/opt/dengdeng` 为工作目录，应用账户为 `dengdeng`，可写路径为 `/var/lib/dengdeng`。如果改变路径，必须同步修改单元和环境文件。

## HTTPS 与真实 IP

仓库的 [Nginx 示例](../deploy/nginx/dengdeng.ganiran.com.ssl.conf) 包含以下关键设置：

- `client_max_body_size 65m`，与应用 64 MiB 公共 API 限制对齐；
- 关闭代理缓冲，避免 SSE 被聚合；
- 读写超时 3600 秒，支持长流式请求；
- 传递 `Host`、`X-Real-IP`、`X-Forwarded-For` 和 `X-Forwarded-Proto`。

复制后先替换域名、证书路径和上游地址，再启用：

```bash
sudo nginx -t
sudo systemctl reload nginx
curl -fsS https://relay.example.com/health
```

服务默认只信任回环代理，并从 `X-Forwarded-For`、`X-Real-IP` 读取客户端 IP。反向代理不在同一主机时，把它的精确 IP 或 CIDR 加入 `SERVER_TRUSTED_PROXIES`。不要信任 `0.0.0.0/0` 或任意公网来源，否则注册限流、IP 规则和用量地区会被伪造请求头绕过。

CDN 或多层代理需要按最靠近应用的可信链路配置。修改真实 IP 设置后，用一次请求核对用量明细中的来源 IP，不要只看 Nginx 访问日志。

## 首次上线

按以下顺序验证：

1. `/health` 返回 `status: ok`，版本和提交号符合预期；
2. 管理员可以登录，默认或临时密码已更换；
3. `SITE_PUBLIC_URL`、登录协议、SMTP、找回密码和 OAuth 回调一致；
4. 创建一个测试分组与上游账号，账号探测和模型发现成功；
5. 用普通用户创建 `dd-` 密钥，执行 `/v1/models`、非流式和流式请求；
6. 用量明细记录 Token、费用、首字耗时、总耗时、分组和账号；
7. 创建并恢复一份备份；
8. 启用支付前验证沙箱或最小真实订单的回调、主动查询、幂等入账和退款。

## SQLite 备份

内置备份服务创建一致性 SQLite 快照。调度器每分钟检查策略，默认每 24 小时创建一份，保留 30 天且最多保留 30 份系统自动快照。管理端修改的策略保存在数据库中，优先于启动默认值。

```dotenv
BACKUP_DIRECTORY=/var/lib/dengdeng/backups
BACKUP_AUTO_ENABLED=true
BACKUP_INTERVAL_HOURS=24
BACKUP_RETENTION_DAYS=30
BACKUP_RETENTION_COUNT=30
```

保留策略只删除创建者为 `system:auto` 的自动快照。管理员手动创建的快照需要手动删除。“立即清理”执行同一套自动快照保留规则，不会删除业务数据或手动备份。

备份文件与生产数据库同盘时不能应对磁盘损坏。至少保留一份异机、对象存储或加密私有仓库副本，并定期执行恢复验证。

## GitHub 加密备份

`deploy/backup/dengdeng-github-backup.sh` 使用 `VACUUM INTO` 创建一致性快照，执行 `PRAGMA integrity_check`，再以 GPG AES-256 对称加密并分片后推送到独立 Git 仓库。默认保留：

- 7 份完整数据库密文；
- 2 份脱敏全栈密文；
- 每片 90 MiB；
- 仓库上限约 900 MiB。

全栈快照包含源码、运行二进制、脱敏数据库、运行环境文件、Nginx/systemd 配置和维护脚本。数据库副本会停用支付与推广提现并删除支付渠道、商户绑定、提现账户和外部交易快照；环境文件中的支付相关值替换为 `REMOVED`。它仍包含用户数据和加密上游凭证，只能写入私有仓库，并且必须在 Git 之外保管解密口令。

安装依赖：`sqlite3`、`gzip`、`gpg`、`git`、`ssh`、`sha256sum`、`split`、`flock`、`tar`。

```bash
sudo install -d -m 0700 -o dengdeng -g dengdeng /var/lib/dengdeng/github-backup
sudo -u dengdeng ssh-keygen -t ed25519 -N '' \
  -f /var/lib/dengdeng/github-backup/id_ed25519 \
  -C dengdeng-github-backup

sudo install -m 0755 deploy/backup/dengdeng-github-backup.sh /usr/local/sbin/
sudo install -m 0755 deploy/backup/dengdeng-github-restore.sh /usr/local/sbin/
sudo install -m 0755 deploy/backup/dengdeng-fullstack-restore.sh /usr/local/sbin/
sudo install -m 0644 deploy/systemd/dengdeng-github-backup.service /etc/systemd/system/
sudo install -m 0644 deploy/systemd/dengdeng-github-backup.timer /etc/systemd/system/
sudo install -m 0640 -o root -g dengdeng \
  deploy/backup/github-backup.conf.example /etc/dengdeng/github-backup.conf

sudo sh -c 'umask 027; openssl rand -base64 48 > /etc/dengdeng/github-backup.pass'
sudo chown root:dengdeng /etc/dengdeng/github-backup.pass
```

把生成的公钥注册为备份仓库的可写 deploy key；把 GitHub 当前公布的 `github.com` 主机公钥写入 `/var/lib/dengdeng/github-backup/known_hosts`，不要用跳过主机校验的 SSH 参数。然后修改 `/etc/dengdeng/github-backup.conf` 中的仓库地址和实际路径。

首次执行并验证数据库恢复：

```bash
sudo systemctl daemon-reload
sudo systemctl start dengdeng-github-backup.service
sudo journalctl -u dengdeng-github-backup.service -n 100 --no-pager

git clone git@github.com:YOUR_ACCOUNT/dengdeng-backups.git /tmp/dengdeng-backups
sudo DENGDENG_BACKUP_PASSPHRASE_FILE=/etc/dengdeng/github-backup.pass \
  /usr/local/sbin/dengdeng-github-restore \
  /tmp/dengdeng-backups latest /tmp/dengdeng-restored.db
sudo sqlite3 /tmp/dengdeng-restored.db 'PRAGMA integrity_check;'
```

验证全栈恢复：

```bash
sudo DENGDENG_BACKUP_PASSPHRASE_FILE=/etc/dengdeng/github-backup.pass \
  /usr/local/sbin/dengdeng-fullstack-restore \
  /tmp/dengdeng-backups latest /tmp/dengdeng-fullstack-restored
sudo sqlite3 /tmp/dengdeng-fullstack-restored/database/dengdeng.sanitized.db \
  'PRAGMA integrity_check;'
```

恢复脚本拒绝覆盖已有文件或目录。通过恢复验证后再启用定时器：

```bash
sudo systemctl enable --now dengdeng-github-backup.timer
systemctl list-timers dengdeng-github-backup.timer
```

## 仓库更新器

管理端版本更新依赖独立 root systemd 单元。网页只创建更新请求并启动固定服务；仓库、分支、源码目录、发布目录和运行二进制路径由 root 独占的 `/etc/dengdeng/update.conf` 决定。

安装前先确认主机实际提供 Go 1.26.5、Node.js 26、pnpm 11.14.0、Git 和 Python 3。随后从已审核的仓库工作树执行：

```bash
sudo bash deploy/update/install.sh
```

默认路径：

```text
/opt/dengdeng/source             受信任源码仓库
/opt/dengdeng/releases           发布状态与旧版本
/opt/dengdeng/dengdeng           当前运行二进制
/var/lib/dengdeng/update         网页与更新器交换状态
/etc/dengdeng/update.conf        root 独占配置
```

更新流程：

1. “检查更新”执行 fetch，并记录当前到目标提交之间的更新记录；
2. “更新”先创建 SQLite 快照，再在源码目录构建前端和后端；
3. 全部构建成功后原子替换运行二进制并重启服务；
4. 连续检查 `/health`；失败则恢复上一个已验证二进制；
5. “回滚”在切换前同样创建数据库快照。

更新日志：

```bash
sudo journalctl -u dengdeng-updater.service -f
```

这是单实例受控更新，切换期间可能有数秒请求失败或重连。它不提供多实例蓝绿发布，也不会自动处理需要人工确认的数据回滚。

## 健康检查与故障定位

```bash
curl -fsS http://127.0.0.1:9100/health
curl -fsS https://relay.example.com/health
sudo journalctl -u dengdeng.service -n 200 --no-pager
```

`/health` 返回：

```json
{
  "status": "ok",
  "version": "v0.1.0",
  "commit": "commit-sha",
  "build_time": "2026-01-01T00:00:00Z"
}
```

排查顺序：

1. 本机 `/health` 失败：检查进程、环境文件、数据库权限和启动日志；
2. 本机成功、公网失败：检查 DNS、证书、Nginx/CDN 和防火墙；
3. 控制台成功、公共 API 失败：检查密钥、分组、账号、模型发现、额度和错误中心；
4. 只有流式失败：检查代理缓冲、超时、SSE Content-Type 和上游空流；
5. 只有大请求 `413`：逐层检查 CDN、WAF、Nginx 和应用请求体上限。

## 生产检查表

- [ ] `JWT_SECRET` 与 `ENCRYPTION_KEY` 独立、随机、已离线保存
- [ ] 管理员使用唯一强密码，TOTP/step-up 策略符合实际运维流程
- [ ] 应用只监听回环或私网，公网入口强制 HTTPS
- [ ] `SITE_PUBLIC_URL` 与 OAuth、支付、提现回调完全一致
- [ ] 只信任真实反向代理 IP，来源 IP 已用实际请求核对
- [ ] 数据库、环境文件、证书和备份口令权限只授予所需账户
- [ ] 支付渠道使用生产商户配置，回调签名和幂等入账已验证
- [ ] 自动备份、异地备份和恢复流程都已实际运行
- [ ] `/health`、日志保留、磁盘空间、证书过期和备份失败有外部监控
- [ ] 上游账号使用方式符合平台授权和服务条款
