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
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags='-s -w' -o dengdeng ./cmd/server
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

## 升级与回滚

1. 先备份 `/var/lib/dengdeng/` 或对应 PostgreSQL 数据库。
2. 构建并上传一个新二进制到临时路径，校验哈希后原子替换 `/opt/dengdeng/dengdeng`。
3. `systemctl restart dengdeng`，检查 `/health`、登录、模型列表和一次真实调用。
4. 若异常，替换回上一个已验证的二进制后重启。数据库迁移前务必保留可恢复备份。

生产 `.env`、数据库、TLS 私钥、支付密钥和上游凭据都只留在服务器或 Secret 管理服务里；不要复制进仓库、截图或构建日志。
