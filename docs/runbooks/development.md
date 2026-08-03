# 本地开发与验证

## Windows

```powershell
.\scripts\verify.ps1
```

脚本会验证 Go、Vue 和 Python Worker；若本机存在 Docker，还会验证 Compose 配置。浏览器冒烟需按下节安装 Playwright 浏览器后独立执行。脚本优先使用 `PATH` 或 `GOROOT` 中的 Go，并兼容 `%USERPROFILE%\go\go1.26.5` 的本地登记路径。

## Go 生命周期

Go 测试覆盖 `signal.NotifyContext` 根 Context、HTTP 与 Runtime 取消、数据库和 WebSocket 收尾，以及取消后的 `retry_waiting`/`failed` 状态。Windows 本地运行：

```powershell
Push-Location .\backend
go test -count=1 ./...
Pop-Location
```

Linux CI 额外执行 `go test -race ./...` 和 `SIGTERM` 进程测试。关机契约固定为 30 秒应用总预算，开发与生产 Compose 的 `stop_grace_period` 固定为 40 秒；超时必须报错退出，不能无限等待。手工运行后按 Ctrl+C 可验证 `SIGINT` 路径，但自动化测试不连接生产服务或使用真实凭据。

PostgreSQL 基线与 Python Worker 共同建立 A1 任务队列协议。Worker 在单事务内用 `FOR UPDATE SKIP LOCKED` 认领，并以数据库时间和唯一 `lease_id` 约束心跳、恢复与终态；当前只执行 `contract.noop` 和 `contract.sleep` 伪任务，不包含数据集、回测或交易能力。

单一基线建立 Outbox 五态、尝试次数、活跃租约、死信/告警时间与索引契约。`internal/db` 使用 PostgreSQL `FOR UPDATE SKIP LOCKED` 提供原子批量认领、数据库时间续租与 fencing、失败重排、过期恢复及告警领取。`drainPendingEvents` 按 `outbox_lease_seconds` 续租、按 `retry_backoff_seconds` 重排，并在尝试耗尽后原子领取死信日志告警。工作流终态与标准事件使用同一短事务，任一事件插入失败会回滚终态。

`internal/service` 契约在随机 PostgreSQL schema 上执行：并发更新必须生成唯一、连续的新版本；并发激活最终只能保留一个 active 版本及其完整入口；激活或停用中途失败必须恢复原 active 版本、入口启停状态与密文；其他连接及运行态 API 只能观察完整旧快照或完整新快照。

A1-5 的通知 WebSocket 定向契约覆盖并发单 writer、固定信封与连续序号、慢连接背压、RFC6455 Ping/Pong、失联超时、Hub 关闭和严格 Origin；前端单元测试覆盖信封版本、重复/倒退序号、重连与同源 URL。无需数据库、真实凭据或外部服务：

```powershell
Push-Location .\backend
go test -count=1 ./internal/service ./internal/api
Pop-Location

Push-Location .\frontend
pnpm exec vitest run src/store/modules/notification.test.ts
Pop-Location
```

Linux CI 的 `go test -race ./...` 负责在竞态检测器下重复覆盖并发推送；严格 Origin 依赖代理保留原始 Host（含端口）和单值 `http/https` 有效 scheme。当前 `token` 查询参数不得进入 Nginx、应用或测试日志，Token 存储与轮换在 A1-7 处理。

Worker 容器可单独验证：

```powershell
$env:COINSPHERE_AUTH__SECRET_KEY = 'local-compose-validation-only'
docker compose build backend worker
docker compose up --detach --no-build --wait worker
docker compose exec -T worker python -m coinsphere_worker health
docker compose down --volumes --remove-orphans
```

Compose 会先等待内部 TimescaleDB 健康，由一次性 `migrate` 应用完整基线，再启动共享该数据库的 Backend 与 Worker。预期 Worker 健康输出包含 `"mode":"a1-postgres"` 和 `"taskConsumer":true`。Worker 不开放端口、不挂载业务数据卷，也不注入交易所凭据。

本机已有仅供测试的 PostgreSQL 时，可直接运行真实并发与取消用例。测试会创建并删除随机隔离 schema，不会清空固定外部表：

```powershell
$env:COINSPHERE_TEST_POSTGRES_DSN = 'postgresql://coinsphere:test-only@127.0.0.1:5432/coinsphere_worker_test?sslmode=disable'
Push-Location .\worker
uv run --frozen pytest tests/test_queue_runtime.py
Pop-Location
```

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

数据库核心契约只在随机隔离的 PostgreSQL schema 中执行。先设置仅供本地测试的 DSN：

```powershell
$env:COINSPHERE_TEST_POSTGRES_DSN = 'postgres://coinsphere:test-only@127.0.0.1:5432/coinsphere_test?sslmode=disable'
Push-Location .\backend
go test -count=1 ./internal/db ./internal/migration ./internal/service ./cmd/migrate
Pop-Location
```

## Linux 与 CI

```bash
./scripts/verify.sh
```

GitHub Actions 负责 Linux、三类镜像构建、Compose 健康、Worker A1 PostgreSQL 集成契约和安全检查。本地缺少 Docker 或 PostgreSQL 时可以继续开发，但 PR 在 Worker 集成与容器 Job 通过前不得合并。

CI 使用固定 TimescaleDB 镜像执行单一基线、Outbox、工作流服务与 Worker 运行时契约，包括空库 Up/Down/重放、非空及并发写入 Down 保护，Worker 七态、租约、并发认领、旧租约 fencing、崩溃回收和 5 秒取消，Outbox 五态、批量与事务失败原子性、续租、退避、过期恢复、死信争抢和旧 token fencing，工作流并发版本、激活回滚和完整快照可见性，以及通知 WebSocket 契约。本地与发布环境的迁移及回滚步骤见[数据库迁移手册](./database-migrations.md)。

## 安全约束

- 本地开发只使用虚假或公开测试数据。
- 不把交易所密钥写入 `.env`、仓库脚本、终端截图或测试输出。
- 任何真实账户验证命令由用户在目标主机执行，并只反馈脱敏结果。
