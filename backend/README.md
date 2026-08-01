# coinsphere Go 后端

> 🔰 **Go 新手请先读 [`GO入门笔记.md`](./GO入门笔记.md)**:从零讲清本项目用到的 Go 语法和框架,代码里的中文注释会引用它的小节名。

原 Python(FastAPI + Peewee + Redis + APScheduler)后端的 Go 重写版。**单二进制、单进程**,同一进程内运行 HTTP API、工作流调度器、执行器与事件分发,不再依赖 Redis 与独立的 orchestrator / worker 进程。

## 架构变化

| 原 Python 版 | Go 版 |
|---|---|
| api / orchestrator / worker / init 四种进程角色 | 一个二进制,启动即完成建表 + 种子数据,goroutine 承载全部后台循环 |
| Redis Stream + 消费组做执行派发 | 数据库即队列:`workflow_executions.status` + 乐观锁 `UPDATE ... WHERE status='queued'` 认领 |
| Redis ZSet 做重试到期索引 | 直接查 `status='retry_waiting' AND next_retry_at <= now` |
| Redis 分布式锁选 leader | 单实例,无需选主(多实例部署需自行加数据库级锁) |
| Redis 并发租约限制同 key 并发 | 进程内信号量(`semaphore_limit_per_key`) |
| APScheduler + pickle 任务表 | 调度循环直接按 runtime entry 的 `next_run_at` 轮询触发(Quartz 6 位 cron / interval / once) |
| PostgreSQL 专属 | GORM AutoMigrate,支持 **SQLite / MySQL / PostgreSQL** |

已删除的表:`scheduler_jobs`(APScheduler pickle)、`workflow_dispatch_outbox`(Redis 中转)。其余表名、列名与 API 响应契约与 Python 版保持一致,前端无需改动。

## 快速开始

```powershell
cd backend
go build -o coinsphere-server.exe .
.\coinsphere-server.exe            # 默认读取 ./config.yml,SQLite,监听 :6987
```

首次启动自动建表并写入种子数据(内置角色/菜单/超管 `coinsphere`/`coinsphere`、两个内置工作流)。

## 版本化数据库迁移

迁移命令与服务端分离，并复用同一份数据库配置：

```powershell
go run ./cmd/migrate -config ./config.yml -direction status
go run ./cmd/migrate -config ./config.yml -direction up
go run ./cmd/migrate -config ./config.yml -direction down -steps 1
go run ./cmd/migrate -config ./config.yml -direction version
```

容器镜像同时提供 `/app/coinsphere-migrate`。A0 阶段迁移 `00001` 只建立版本历史，不替代现有业务表的 GORM `AutoMigrate`；应用启动切换属于 A1 独立交付。迁移编写、验证和回滚约束见 [`docs/runbooks/database-migrations.md`](../docs/runbooks/database-migrations.md)。

## 数据库切换

`config.yml` 的 `database.driver` 支持 `sqlite` / `mysql` / `postgres`:

```yaml
database:
  driver: postgres
  host: 127.0.0.1
  port: 5432
  user: coinsphere
  password: secret
  database: coinsphere
  schema: coinsphere    # 仅 postgres
```

任意配置可用环境变量覆盖,规则 `COINSPHERE_<段>__<键>`:

```powershell
$env:COINSPHERE_DATABASE__DRIVER = 'mysql'
$env:COINSPHERE_SERVER__PORT = '7000'
```

- SQLite:纯 Go 驱动(免 cgo),开启 WAL,单写连接;适合单机/开发。
- PostgreSQL:自动创建并使用 `database.schema` 指定的 schema,已实测端到端通过。
- MySQL:GORM 官方驱动 + AutoMigrate,DSN 自动带 `parseTime=true`;未在本机实测。

> 密码哈希(pbkdf2_sha256)、JWT 格式、Fernet 加密与 Python 版兼容;但 Go 版删除了两张表且不迁移旧 Redis 状态,建议指向全新 schema/库,而不是直接复用 Python 版正在使用的 schema。

## 运行时行为

- **调度**:每秒扫描 `next_run_at` 到期的 schedule 入口,先乐观推进 `next_run_at` 抢占触发权,再以 `schedule:{entryId}:{dueUnix}` 幂等键入队。
- **执行**:dispatcher 认领 queued 执行(每 key 并发受限),worker goroutine 跑图并写节点/边日志,心跳写 `last_heartbeat_at`。
- **重试**:可重试失败(timeout/connection/429/5xx)按 `retry_backoff_seconds` 退避,`retry_waiting → queued` 自动提升。
- **恢复**:心跳超时(含进程崩溃重启后的孤儿执行)标记 `worker_lost` 并按剩余次数重试或失败。
- **事件**:领域事件写 `domain_event_outbox`,后台循环投递,匹配 `start.event` 入口自动触发工作流。
- **清理**:每天 03:00 后按批删除超过保留期的终态执行。

## 目录

```
main.go                 入口:配置 → 建表种子 → 运行时 → HTTP
cmd/migrate             独立版本化 SQL migration 命令
internal/config         YAML + 环境变量覆盖
internal/db             GORM 模型 / 多方言 Open / 种子数据
internal/migration      嵌入式 SQL、Goose Runner 与迁移契约
internal/security       pbkdf2 密码、HS256 token、Fernet 密文
internal/perm           权限码常量与内置菜单映射
internal/service        全部业务逻辑(App 结构,按领域分文件)
  loops.go              调度/派发/事件/恢复/清理 goroutine
  engine.go nodes.go    图执行引擎与节点注册表
  runtime.go            激活/入口/入队/查询
internal/api            路由、中间件、handlers(响应契约同 Python 版)
```
