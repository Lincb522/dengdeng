# 开发与贡献

提交应修复一个可说明、可验证的问题。接口、配置、计费和部署行为以当前代码为准；修改这些边界时必须同步更新对应文档。

## 环境

| 工具 | 仓库版本 |
| --- | --- |
| Go | 1.26.5 |
| Node.js | 26 |
| pnpm | 11.14.0 |

可选工具：Docker、SQLite CLI、PostgreSQL、`jq`、`curl`、`govulncheck`。

## 获取依赖

```bash
git clone https://github.com/Lincb522/dengdeng.git
cd dengdeng

corepack enable
cd frontend
pnpm install --frozen-lockfile

cd ../backend
go mod download
```

不要提交 `node_modules`、本地数据库、前端构建产物、二进制、证书或环境文件。

## 本地运行

后端：

```bash
cd backend
JWT_SECRET="$(openssl rand -hex 32)" \
ENCRYPTION_KEY="$(openssl rand -hex 32)" \
ADMIN_EMAIL=admin@dengdeng.local \
ADMIN_PASSWORD=local-admin-password \
go run ./cmd/server
```

Vue 控制台：

```bash
cd frontend
pnpm dev
```

React 图像工作台：

```bash
cd frontend
pnpm dev:workbench
```

默认地址：

- 后端：`http://127.0.0.1:9100`
- Vue：`http://127.0.0.1:5173`
- Workbench：`http://127.0.0.1:5174/image-workbench/`

本地示例密码不得用于共享或公网环境。

## 代码结构

```text
backend/cmd/server              进程入口
backend/internal/server         依赖组装和路由
backend/internal/gateway        公共模型 API
backend/internal/handler        控制台 API
backend/internal/service        领域服务和后台任务
backend/internal/model          持久化模型
backend/internal/payment        支付渠道
frontend/src                    Vue 控制台
frontend/workbench              React 图像工作台
deploy                          部署与运维脚本
```

修改前先找到状态和事务的现有所有者。不要在 handler、组件或脚本中复制第二套计费、密钥解密、订单结算、调度或配置逻辑。

## 验证

### 后端

```bash
cd backend
go test ./...
go vet ./...
```

改变依赖或请求边界时再运行：

```bash
cd backend
go mod verify
go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...
```

### 前端

```bash
cd frontend
pnpm typecheck:workbench
pnpm build
```

用户界面修改必须在真实页面验证，不以编译成功代替：

- 最窄支持宽度和桌面宽度；
- 浅色与深色；
- 空、加载、失败、长文本和大量数据；
- 键盘焦点和主要操作；
- 两侧是否出现非预期横向溢出。

### 端到端脚本

`scripts/e2e.sh` 面向隔离测试数据库和 Mock 上游，默认假定：

- 主服务在 `127.0.0.1:9100`；
- Mock 上游在 `127.0.0.1:9200`；
- 测试管理员为 `admin@test.local`；
- 允许脚本创建和修改用户、分组、账号、密钥、余额与兑换码。

它会执行破坏性测试数据操作，不能指向生产环境。准备好隔离环境后运行：

```bash
BASE=http://127.0.0.1:9100 \
MOCK=http://127.0.0.1:9200 \
DENGDENG_DB=/tmp/dengdeng-e2e/test.db \
bash scripts/e2e.sh
```

只改文档时至少执行链接检查和 `git diff --check`。

## 数据库变更

- GORM 模型是表结构的主要来源；升级通过启动时 AutoMigrate 和必要的显式数据迁移完成。
- 新字段必须定义旧行的实际语义，不能只依赖查询时的零值掩盖缺失数据。
- 金额使用整数 micro-USD 或渠道最小货币单位，不使用浮点数落账。
- 多步骤余额、额度、订单、佣金和提现变更必须在同一数据库事务中完成。
- 外部请求不能处于数据库事务锁内；先持久化可重试状态，再执行外部调用并幂等结算。
- 同时验证 SQLite 和 PostgreSQL 可执行的 SQL；SQLite 专用维护逻辑必须明确返回“不支持 PostgreSQL”。

## API 与错误

- 公共模型 API 返回相应协议可识别的错误对象和真实 HTTP 状态。
- 未知 API 路径不能回退为 SPA 的 HTTP 200 HTML。
- 流式响应在首个有效输出前可以故障转移；开始输出后不得拼接第二个账号的响应。
- 错误信息不得包含完整 API Key、OAuth Token、私钥、Cookie、支付秘密或未裁剪请求正文。
- 新错误应归入请求级、模型级或账号级，避免把客户端参数错误错误地冷却整个账号池。

公共入口变化同步修改 [API 文档](docs/API.md)；请求生命周期变化同步修改 [架构文档](docs/ARCHITECTURE.md)。

## 配置与秘密

可提交的配置只有模板。以下内容不得进入 Git、Issue、测试快照或构建日志：

- `.env`、`config.yaml`；
- SQLite/PostgreSQL 数据；
- `dd-` 密钥和上游凭证；
- OAuth Client Secret、SMTP 密码；
- 支付商户私钥、API v3 Key、Webhook Secret；
- TLS、SSH、GPG 和备份密钥。

测试使用随机生成的假值、Mock 上游和隔离数据库。不要通过读取生产 Secret 来验证新脚本。

## 提交

1. 保持改动范围与问题一致，删除由本次修改产生的无用代码和文档。
2. 使用能说明最终行为的提交信息，例如 `fix: preserve key route selection`。
3. 用户可见变化加入 `CHANGELOG.md` 的“未发布”。
4. 新增第三方源码、资产或数据源时更新 `NOTICE.md`，并保留上游许可证。
5. 提交前检查：

   ```bash
   git diff --check
   git status --short
   ```

6. PR 说明只写最终行为、必要的兼容边界和无法由 diff 看出的风险，不记录已放弃的实现过程。

## 文档约定

- 用中文描述项目行为，代码、字段、路径和协议名保留原名。
- 每项能力必须能在当前路由、配置、模型或测试中找到依据。
- 不把计划、参考项目能力或上游宣传写成已实现功能。
- 默认值、版本、路径和命令修改时更新唯一拥有它的文档，其他页面只链接。
- 示例使用不可用的占位域名和密钥，不复制真实日志或用户数据。
