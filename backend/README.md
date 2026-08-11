# coinsphere Go 后端

> 🔰 **Go 新手请先读 [`GO入门笔记.md`](./GO入门笔记.md)**:从零讲清本项目用到的 Go 语法和框架,代码里的中文注释会引用它的小节名。

原 Python(FastAPI + Peewee + Redis + APScheduler)后端的 Go 重写版。Go App 在单进程内运行 HTTP API、工作流调度器与事件分发；数据库 migration 和 Executor 使用同一镜像中的独立二进制。量化策略由独立 Python Worker 执行，不再依赖 Redis 或旧 orchestrator。

## 架构变化

| 原 Python 版 | Go 版 |
|---|---|
| api / orchestrator / worker / init 四种进程角色 | 一个 Go App 承载 API 与后台循环；独立 migration 二进制拥有 DDL；Go Executor 和 Python Worker 使用受限独立进程 |
| Redis Stream + 消费组做执行派发 | 数据库即队列:`workflow_executions.status` + 乐观锁 `UPDATE ... WHERE status='queued'` 认领 |
| Redis ZSet 做重试到期索引 | 直接查 `status='retry_waiting' AND next_retry_at <= now` |
| Redis 分布式锁选 leader | 单实例,无需选主 |
| Redis 并发租约限制同 key 并发 | 进程内信号量(`semaphore_limit_per_key`) |
| APScheduler + pickle 任务表 | 调度循环直接按 runtime entry 的 `next_run_at` 轮询触发(Quartz 6 位 cron / interval / once) |
| PostgreSQL 专属 | **PostgreSQL/TimescaleDB-only** 的版本化 SQL 基线 |

## 快速开始

```powershell
cd backend
$env:COINSPHERE_DATABASE__DSN = 'postgresql://coinsphere:test-only@127.0.0.1:5432/coinsphere?sslmode=disable'
go run ./cmd/migrate -config ./config.yml -direction up
go build -o coinsphere-server.exe .
.\coinsphere-server.exe            # 默认读取 ./config.yml，监听 :6987
```

独立命令必须先把空 schema 应用到当前版本；服务只读校验 migration 版本，不会执行 DDL。版本缺失、落后或领先时服务明确拒绝启动。首次成功启动写入内置角色、菜单、超管 `coinsphere`/`coinsphere` 和两个内置工作流。

## 版本化数据库迁移

迁移命令与服务端分离，并复用同一份数据库配置：

```powershell
go run ./cmd/migrate -config ./config.yml -direction status
go run ./cmd/migrate -config ./config.yml -direction up
go run ./cmd/migrate -config ./config.yml -direction down -steps 1
go run ./cmd/migrate -config ./config.yml -direction version
```

容器镜像同时提供 `/app/coinsphere-migrate` 和 `/app/coinsphere-executor`。版本化 SQL 在空 PostgreSQL schema 中建立当前业务表、`worker_tasks`、Paper 交易事件/投影、外键、索引和状态约束。项目尚未投产，旧开发数据直接重置，不提供 SQLite、MySQL 或旧 PostgreSQL schema 的升级路径。迁移编写、验证和回滚约束见 [`docs/runbooks/database-migrations.md`](../docs/runbooks/database-migrations.md)。

## 数据库配置

`config.yml` 只保留 PostgreSQL DSN 和连接池参数：

```yaml
database:
  dsn: postgresql://coinsphere:secret@127.0.0.1:5432/coinsphere?sslmode=disable
  max_open_conns: 40
  max_idle_conns: 10
  conn_max_idle_time_seconds: 300
```

任意配置可用环境变量覆盖,规则 `COINSPHERE_<段>__<键>`:

```powershell
$env:COINSPHERE_DATABASE__DSN = 'postgresql://coinsphere:test-only@127.0.0.1:5432/coinsphere?sslmode=disable'
$env:COINSPHERE_SERVER__PORT = '7000'
```

DSN 必须指向已经存在的数据库和全新 CoinSphere schema；连接入口不会创建 schema。

## 运行时行为

- **版本与激活**:同一工作流 family 的版本生成、激活、停用和运行时入口替换在数据库事务内串行；并发更新生成唯一、单调递增版本，失败保留原 active 版本及完整入口，读取只返回完整旧快照或完整新快照。
- **调度**:每秒扫描 `next_run_at` 到期的 schedule 入口,先乐观推进 `next_run_at` 抢占触发权,再以 `schedule:{entryId}:{dueUnix}` 幂等键入队。
- **执行**:dispatcher 认领 queued 执行(每 key 并发受限),worker goroutine 跑图并写节点/边日志,心跳写 `last_heartbeat_at`。
- **生命周期**:`signal.NotifyContext` 将 `SIGINT`/`SIGTERM` 统一传给 HTTP、Runtime、数据库和 WebSocket；应用最多用 30 秒收尾，Compose 提供 40 秒宽限。
- **通知 WebSocket**:`/api/v1/ws/notifications` 每连接使用有限队列和唯一 writer，统一写固定五字段信封与 RFC6455 Ping；业务序号从 1 连续递增，慢连接摘除不阻塞健康连接，Origin 必须与有效 scheme、Host 和端口同源，关机等待 writer 退出。
- **取消**:停止后不再接收请求或认领执行；被取消的既有执行按当前重试策略进入 `retry_waiting` 或 `failed`。
- **重试**:可重试失败(timeout/connection/429/5xx)按 `retry_backoff_seconds` 退避,`retry_waiting → queued` 自动提升。
- **恢复**:心跳超时(含进程崩溃重启后的孤儿执行)标记 `worker_lost` 并按剩余次数重试或失败。
- **事件**:工作流终态与标准领域事件在同一短事务写入 `domain_event_outbox`；PostgreSQL 存储层使用 `FOR UPDATE SKIP LOCKED` 原子批量认领，并用数据库时间续租和 fencing 投递。匹配 `start.event` 入口后以稳定幂等键触发工作流；订阅失败按 `retry_backoff_seconds` 重排，尝试耗尽进入死信，未告警死信由 `alerted_at` 原子去重后输出脱敏日志。
- **Executor**:默认只按账户串行处理 Paper 意图和可重建投影。`COINSPHERE_TRADING__TESTNET_PRIVATE_API_ENABLED=true` 可独立装配 Testnet；Spot Live manual 由 `COINSPHERE_TRADING__SPOT_LIVE_MANUAL_ENABLED=true` 装配，USD-M Live manual 由 `COINSPHERE_TRADING__USD_M_LIVE_MANUAL_ENABLED=true` 装配，两个市场使用隔离客户端。Spot auto 还必须同时设置 `COINSPHERE_TRADING__SPOT_LIVE_AUTO_ENABLED=true`。私有运行时要求安全的 `auth.encryption_key`，默认和生产配置均保持关闭。Live manual 要求 Owner 恢复放行；Spot Live auto 另要求管理员授权和 Owner 独立放行。USD-M Live 只允许 manual，并持续验证逐仓、单向、低杠杆、标记价与强平距离。暂停、凭据/风控变化、对账差异或急停会撤销 Owner 放行。CI、Codex、自动部署和工作流不得提供 Live 凭据或启用 Live 开关；Binance 环境验证延期到全部开发完成后，当前只验 Paper 和离线契约。
- **清理**:每天 03:00 后按批删除超过保留期的终态执行。

Python Worker 通过独立 PostgreSQL 连接消费 `worker_tasks`，使用唯一租约完成认领、心跳、崩溃回收和 5 秒内取消。生产 Release 构建并部署 realtime/backtest Worker 与 Paper Executor；二者均使用专用数据库身份且不持有真实交易凭据。

工作流版本、激活、Outbox 和 Worker 契约都在随机隔离的 PostgreSQL schema 上验证。设置测试 DSN 后运行：

```powershell
$env:COINSPHERE_TEST_POSTGRES_DSN = 'postgres://coinsphere:test-only@127.0.0.1:5432/coinsphere_test?sslmode=disable'
go test -count=1 ./internal/db ./internal/migration ./internal/service ./cmd/migrate
```

通知 WebSocket 不依赖数据库方言，可独立验证并发写、序号、背压、心跳、关闭和 Origin：

```powershell
go test -count=1 ./internal/service ./internal/api
```

## 目录

```
main.go                 入口:根 Context → 配置 → 版本校验/种子 → Runtime/HTTP → 有界关机
cmd/migrate             独立版本化 SQL migration 命令
cmd/executor            Paper 执行与默认关闭的 Testnet/Spot Live 私有运行时
internal/exchange       Executor 专属的 Binance 私有协议
internal/config         YAML + 环境变量覆盖
internal/db             GORM 模型 / PostgreSQL 连接 / 种子数据
internal/migration      嵌入式 SQL、Goose Runner 与迁移契约
internal/security       pbkdf2 密码、HS256 token、Fernet 密文
internal/perm           权限码常量与内置菜单映射
internal/service        全部业务逻辑(App 结构,按领域分文件)
  loops.go              调度/派发/事件/恢复/清理 goroutine
  engine.go nodes.go    图执行引擎与节点注册表
  runtime.go            激活/入口/入队/查询
  trading.go            Paper 账户、风险、急停与意图契约
  paper_executor.go     Paper 执行、追加事件与投影重建
internal/api            路由、中间件、handlers
```
