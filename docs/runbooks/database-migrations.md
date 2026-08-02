# 数据库迁移与回滚

## 当前边界

A0 已提供独立的版本化 SQL migration 命令、嵌入式迁移文件、PostgreSQL advisory lock 和自动化契约测试。A1 的 `00002_a1_worker_tasks.sql` 新建独立 Worker 任务队列表；服务进程仍通过 `db.Open` 对既有业务模型执行 GORM `AutoMigrate`，迁移命令通过 `db.Connect` 建立连接且不会触发 AutoMigrate。

迁移命令只支持 SQLite 开发态和目标 PostgreSQL 架构。现有 MySQL 应用启动路径继续使用 `AutoMigrate`，不在本迁移工具的支持与验收范围内。

`00001_a0_versioned_migrations.sql` 是机制基线，只建立 `schema_migrations` 版本历史。`00002` 只建立 `worker_tasks`，不启用 Python Worker、API 或 Compose 数据库连接。A1 仍必须在路线图第 10 项用独立 PR 完成其余业务 schema 的 SQL 基线、存量数据库校准、启动路径切换和恢复演练；在该 PR 合并前不得关闭 `AutoMigrate`。

## 命令

在 `backend` 目录执行：

```bash
go run ./cmd/migrate -config ./config.yml -direction status
go run ./cmd/migrate -config ./config.yml -direction version
go run ./cmd/migrate -config ./config.yml -direction up
go run ./cmd/migrate -config ./config.yml -direction up -target 2
go run ./cmd/migrate -config ./config.yml -direction down -steps 1
```

默认超时为 5 分钟，可用 `-timeout 10m` 调整。进程接收 Ctrl+C 或 SIGTERM 后会取消操作。`down` 会先确认有足够的已应用迁移，防止请求步数过大时只完成部分回滚。

每个迁移文件各自在事务中提交，多文件批次不是一个总事务。后续文件失败时，CLI 会先输出本批次已经成功的版本再报错；此时必须重新执行 `version` 和 `status`，不得假定整个批次都已回滚。

当数据库版本高于当前二进制内置的最新版本时，`up`、`down` 和 `status` 会拒绝执行，防止旧镜像误操作新 schema；`version` 仍可用于确认版本差异。

镜像内命令位于 `/app/coinsphere-migrate`。当前 Compose 可用于开发验证：

```bash
COINSPHERE_AUTH__SECRET_KEY=local-migration-only \
  docker compose run --rm backend /app/coinsphere-migrate -config /app/config.yml -direction status
```

真实发布命令必须由用户在 Linux 主机执行，且只使用 Docker Secret 或本机配置注入数据库凭据。凭据不得出现在仓库、Issue、PR、CI 参数或终端截图中。

## 编写迁移

- 文件位于 `backend/internal/migration/sql`，命名为五位递增版本号加说明，例如 `00003_platform_schema.sql`。
- 每个文件必须同时包含 `-- +goose Up` 和 `-- +goose Down`。
- 已合并或在任何环境应用过的迁移禁止修改、重排或复用版本号；修正只能追加新迁移。
- 默认保持事务执行。确需非事务 DDL 时必须单独 PR，写明失败后的恢复步骤并经过 PostgreSQL 演练。
- 一个迁移 PR 只处理一个 schema 行为。破坏性删除采用“扩展、回填、切换、收缩”多阶段流程。
- 金融时间使用 UTC `timestamptz`，价格、金额、数量和费率使用 `numeric(38,18)`；不得引入账务 `float`。
- `Down` 必须保护仍需保留的数据。无法无损回滚时，明确声明只能通过备份恢复，不能提供伪可逆 SQL。

## 自动化验证

```bash
cd backend
go test -count=1 ./internal/migration ./cmd/migrate
```

默认测试在临时 SQLite 文件执行。设置 `COINSPHERE_TEST_POSTGRES_DSN` 后，同一套契约还会在隔离 PostgreSQL schema 中执行；该测试账户需要创建和删除 schema 的权限。GitHub Actions 强制设置该变量并覆盖：

- 空库升级到最新版本。
- 指定旧版本升级到最新且保留数据。
- 回滚一步后重新升级。
- 重复升级不改变版本或业务数据。
- 失败迁移文件事务回滚，不留下该文件的部分 schema，也不推进该文件版本。
- 超量回滚与无效目标版本在修改 schema 前失败。
- CLI 使用无 `AutoMigrate` 的连接入口。
- `worker_tasks` 只接受公共契约的七种状态，强制尝试次数范围、活跃状态完整租约、租约 ID 唯一，以及取消和终态时间一致性。
- `00002` 的 Down 只允许空队列；存在任何任务时必须失败并保持表、数据和 migration 版本不变。

## 发布步骤

1. 停止会写入受影响表的任务；交易阶段还必须禁止新增敞口。
2. 记录固定镜像版本和 Commit SHA，执行数据库备份并验证备份可读取。
3. 执行 `version` 和 `status`，确认当前版本符合 PR 的升级前置条件。
4. 在研究环境运行 `up`，检查迁移输出、应用就绪状态和关键数据校验。
5. 用户审查结果后，手工启动目标版本服务。

## 回滚步骤

1. 立即停止相关写入，记录失败版本、请求时间和错误输出。
2. 若 PR 明确声明 Down 可逆，先在备份副本演练，再执行固定步数的 `down`。
3. 若迁移包含不可逆数据变换或 Down 失败，停止继续尝试，从发布前备份恢复。
4. 恢复上一固定镜像，运行 `version`、就绪检查和领域数据校验。
5. 禁止手工删除或修改 `schema_migrations` 记录；这会让代码版本与实际 schema 脱节。

## 本次交付回滚

本次 `00002` 只创建尚未被运行时使用的 `worker_tasks`。回滚前先停止相关写入并确认 `SELECT COUNT(*) FROM worker_tasks` 为零，再使用包含 `00002` 的迁移二进制执行一次 `down`，确认版本回到 `1` 后才恢复上一镜像。若表非空，Down 会 fail-closed；不得删除任务或修改 `schema_migrations` 绕过保护，应保留当前版本并调查数据来源，必要时从发布前备份恢复。
