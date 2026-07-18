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

## 服务器直连仓库与版本更新

二进制 + systemd 部署可以安装独立更新器，让管理员在「系统维护 → 版本更新」检查 `main`、执行更新或恢复上一版本。源码固定放在 `/opt/dengdeng/source`，运行二进制仍是 `/opt/dengdeng/dengdeng`；前端和后端全部构建成功前不会触碰线上进程。

首次安装需要 root 权限：

```bash
cd /path/to/dengdeng
sudo bash deploy/update/install.sh
```

安装脚本会完成以下一次性配置：

- 安装 Git、Go、Node.js、pnpm 与 Python 3，并克隆受信任仓库；
- 安装 `dengdeng-updater.service` 和 `/usr/local/sbin/dengdeng-update`；
- 写入最小化 Polkit 规则：应用账户只能启动固定 updater 单元，不能传入命令或仓库地址；
- 开启 `UPDATE_ENABLED`，重启主服务以显示管理端入口。

仓库和分支由 root 独占的 `/etc/dengdeng/update.conf` 决定。调整后无需把 GitHub 凭据交给网页；私有仓库请只给 root 的 Git 客户端配置只读 deploy key。日常流程如下：

1. 「检查更新」只执行 `git fetch` 并比较提交，不重启服务。
2. 「更新到最新版本」先通过应用创建一致性 SQLite 快照，再在独立 systemd 任务中构建。
3. 构建成功后原子替换二进制，短暂重启并连续检查 `/health`。
4. 健康检查失败会自动恢复旧二进制；「恢复上一版本」也会在切换前创建数据库快照。

当前部署是单实例监听 `127.0.0.1:9100`，版本切换期间通常会有数秒连接重试窗口。它是受控热更新，不承诺多实例蓝绿架构才具备的绝对零停机。更新状态保存在 `/var/lib/dengdeng/update/status.json`，详细构建输出使用：

```bash
sudo journalctl -u dengdeng-updater.service -f
```

若不启用网页更新，仍可使用手工流程：先创建数据库快照，构建并上传临时二进制，校验后原子替换，重启并检查 `/health`；异常时替换回上一已验证二进制。

生产 `.env`、数据库、TLS 私钥、支付密钥和上游凭据都只留在服务器或 Secret 管理服务里；不要复制进仓库、截图或构建日志。
