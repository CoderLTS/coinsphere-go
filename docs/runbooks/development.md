# 本地开发与验证

## Windows

```powershell
.\scripts\verify.ps1
```

脚本会验证 Go、Vue 和 Python Worker；若本机存在 Docker，还会验证 Compose 配置。浏览器冒烟需按下节安装 Playwright 浏览器后独立执行。脚本优先使用 `PATH` 或 `GOROOT` 中的 Go，并兼容 `%USERPROFILE%\go\go1.26.5` 的本地登记路径。

Worker 容器可单独验证：

```powershell
$env:COINSPHERE_AUTH__SECRET_KEY = 'local-compose-validation-only'
docker compose build worker
docker compose up --detach --no-build --wait worker
docker compose exec -T worker python -m coinsphere_worker health
docker compose rm --stop --force worker
```

预期健康输出包含 `"mode":"a0-idle"` 和 `"taskConsumer":false`。A0 Worker 不连接数据库、不领取任务，开发 Compose 也不会为其提供网络、端口、卷或凭据。

## 前端浏览器冒烟

Playwright 版本和对应浏览器修订号由 `pnpm-lock.yaml` 锁定。本地首次运行或锁定版本变化后安装三个浏览器，再执行关键冒烟：

```powershell
Push-Location .\frontend
pnpm install --frozen-lockfile
pnpm exec playwright install chromium firefox webkit
pnpm test:e2e
Pop-Location
```

配置会先生成不写入自动导入声明、且不包含 Vue DevTools 的新鲜 E2E 构建，再在 `127.0.0.1:4173` 固定端口启动 Vite production preview；已有端口服务不会被复用。测试使用真实浏览器交互验证两条现有路径：游客访问工作流创建页时进入 403，以及已认证会话填写基础信息、校验并保存默认工作流。后端 API 和通知 WebSocket 由 Playwright 路由 Mock 隔离，未声明的 API 与非本机 HTTP 请求会被阻断；执行不需要公网、真实凭据、后端、Compose 或生产数据。

单浏览器排障和失败 trace 查看命令如下：

```powershell
pnpm exec playwright test --project=chromium
pnpm exec playwright show-trace .\test-results\<失败用例>\trace.zip
```

CI 使用锁定依赖安装 Chromium、Firefox、WebKit，并在 `Playwright browser smoke` 阻塞检查中运行。截图、trace 和 HTML 报告只在检查失败时上传，保留 7 天。Chromium 项目不是 Microsoft Edge 品牌浏览器；WebKit 项目也不是 macOS Safari 或 iOS 真机 Safari，不能替代 Edge 企业策略、Safari 系统集成、iOS 触控和真机兼容性验收。

本门禁不改变业务接口、数据库、Compose 或部署产物。需要回滚时整体回退引入 Playwright 的 PR，删除测试依赖、配置、用例和 CI Job，并从 `Container builds` 的 `needs` 及仓库 Required checks/ruleset 中同步移除 `Playwright browser smoke`；无数据或运行时迁移。

迁移契约默认在临时 SQLite 数据库执行。要同时验证 PostgreSQL，先设置仅供本地测试的 DSN：

```powershell
$env:COINSPHERE_TEST_POSTGRES_DSN = 'postgres://coinsphere:test-only@127.0.0.1:5432/coinsphere_test?sslmode=disable'
Push-Location .\backend
go test -count=1 ./internal/migration ./cmd/migrate
Pop-Location
```

## Linux 与 CI

```bash
./scripts/verify.sh
```

GitHub Actions 负责 Linux、三类镜像构建、Compose 健康、Worker A0 契约和安全检查。本地缺少 Docker 时可以继续开发，但 PR 在容器 Job 通过前不得合并。

CI 使用固定 PostgreSQL 17 镜像执行迁移契约。本地与发布环境的迁移命令、编写约束和回滚步骤见[数据库迁移手册](./database-migrations.md)。

## 安全约束

- 本地开发只使用虚假或公开测试数据。
- 不把交易所密钥写入 `.env`、仓库脚本、终端截图或测试输出。
- 任何真实账户验证命令由用户在目标主机执行，并只反馈脱敏结果。
