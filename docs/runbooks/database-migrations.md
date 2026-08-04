# 数据库迁移与回滚

## 当前边界

CoinSphere 只支持 PostgreSQL/TimescaleDB。Backend 与 Python Worker 共用同一数据库和 schema，DDL 只由独立 migration 命令执行；服务启动只读校验 migration 版本，不执行 `AutoMigrate`。

`00001_a1_postgres_baseline.sql` 面向全新空 schema，一次建立当前业务表、`worker_tasks`、Outbox 状态约束、外键和索引。`00002_a1_observability.sql` 增加 `audit_records`；表内存在记录时 Down 会失败并保留表、数据和 migration 版本。项目尚未投产，旧 SQLite、MySQL 或旧 PostgreSQL 开发数据直接重置；基线不会识别、保留或升级旧 schema。

数据库配置只保留 DSN 与连接池参数：

```yaml
database:
  dsn: postgresql://coinsphere:password@127.0.0.1:5432/coinsphere?sslmode=disable
  max_open_conns: 40
  max_idle_conns: 10
  conn_max_idle_time_seconds: 300
```

生产 DSN 只通过主机上的 `runtime.env` 或等价 Secret 注入，不得进入仓库、Issue、PR、CI 参数、日志或截图。

## 命令

在 `backend` 目录执行：

```bash
go run ./cmd/migrate -config ./config.yml -direction status
go run ./cmd/migrate -config ./config.yml -direction version
go run ./cmd/migrate -config ./config.yml -direction up
go run ./cmd/migrate -config ./config.yml -direction up -target 1
go run ./cmd/migrate -config ./config.yml -direction down -steps 1
```

默认超时为 5 分钟，可用 `-timeout 10m` 调整。Ctrl+C 或 SIGTERM 会取消操作。`down` 会先确认有足够的已应用版本，避免超量请求只完成部分回滚。

每个 migration 文件在独立事务中提交，多文件批次不是一个总事务。后续文件失败时，CLI 会先输出已经成功的版本；必须重新执行 `version` 和 `status`，不能假定整个批次已回滚。

数据库版本高于当前二进制内置最新版本时，`up`、`down` 和 `status` 会拒绝执行；`version` 仍可读取差异。镜像内命令位于 `/app/coinsphere-migrate`。

开发 Compose 由一次性 `migrate` 服务在共享 TimescaleDB 上执行一次 Up，成功后 Backend 与 Worker 才启动：

```bash
COINSPHERE_AUTH__SECRET_KEY=local-migration-only docker compose up -d --build
docker compose run --rm migrate /app/coinsphere-migrate -config /app/config.yml -direction status
```

## 编写迁移

- 文件位于 `backend/internal/migration/sql`，使用五位递增版本号，例如 `00002_market_data.sql`。
- 每个文件同时包含 `-- +goose Up` 和 `-- +goose Down`。
- 已合并或在共享环境应用过的文件禁止修改、重排或复用版本号；修正只能追加 migration。
- 默认保持事务执行。确需非事务 DDL 时必须使用独立 PR，写明失败恢复步骤并完成 PostgreSQL 演练。
- 一个 migration PR 只处理一个 schema 行为。破坏性删除采用“扩展、回填、切换、收缩”多阶段流程。
- 金融时间使用 UTC `timestamptz`；价格、金额、数量和费率使用 `numeric(38,18)`，不得使用浮点账务列。
- `Down` 必须保护数据。无法无损回滚时明确声明只允许恢复备份，不能提供伪可逆 SQL。

## 自动化验证

```bash
cd backend
COINSPHERE_TEST_POSTGRES_DSN='postgres://coinsphere:test-only@127.0.0.1:5432/coinsphere_test?sslmode=disable' \
  go test -count=1 ./internal/db ./internal/migration ./internal/service ./cmd/migrate
```

测试账户必须能创建和删除随机隔离 schema。CI 使用固定 TimescaleDB 镜像并覆盖：

- 空 schema 应用全部内置 migration、重复 Up、空库 Down 和重新 Up。
- 审计字段约束、外键、索引以及非空审计表的 Down 保护。
- 服务启动对未迁移、落后或领先版本 fail-fast，且不执行 DDL。
- 基线包含全部当前表、Worker 七态约束、Outbox 五态/租约/终态约束及必要索引。
- 非空 schema 的 Down 保留全部表、数据和 migration 版本。
- Down 在计数前锁住全部业务表；并发写入提交后必须被空表 guard 看见并拒绝回滚。
- Outbox 并发认领、事务回滚、续租、失败退避、过期恢复、死信告警争抢和旧 token fencing。
- 工作流并发版本、激活/停用失败回滚和一致快照可见性。
- Worker 并发认领、心跳、崩溃恢复、尝试耗尽和 5 秒取消契约。

## 发布

1. 停止会写入受影响表的服务；交易阶段还必须禁止新增敞口。
2. 记录固定镜像和 Commit SHA，创建并验证 PostgreSQL 备份。
3. 执行 `version` 与 `status`，确认升级前置版本。
4. 使用目标镜像内的 migration 命令执行 Up，记录结果。
5. 启动目标服务，检查就绪状态和关键数据约束。

## 回滚

代码回滚与 schema 回滚分开处理：

1. 代码或健康检查失败时停止新版本，恢复上一固定镜像与 Compose，保留当前 schema 和 `schema_migrations`。
2. 只有 PR 明确声明 Down 可逆时，才在备份副本演练后执行固定步数 Down。
3. 基线 Down 只适用于从未产生任何业务数据的 schema。执行前停止 Backend、Worker 和所有其他写入者；Down 会锁住全部业务表并再次检查为空。
4. 任一表非空或 Down 失败时停止尝试，保留现场并从发布前备份恢复。禁止删除业务行、手工修改 `schema_migrations` 或跳过版本。
5. 恢复后重新执行 `version`、就绪检查和领域数据校验。

部署脚本不会自动执行 Down，也不会自动覆盖 PostgreSQL 数据。旧 SQLite 卷不会被新 Compose 挂载或删除，需要时由管理员在确认不再回滚旧版本后单独清理。
