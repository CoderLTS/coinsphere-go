# 数据库迁移与回滚

## 当前边界

CoinSphere 只支持 PostgreSQL/TimescaleDB。DDL 只由独立 Migrator 身份和 migration 命令执行；服务启动只读校验版本，不执行 `AutoMigrate`。普通角色与 Worker 首期共用受限应用身份，Executor 使用独立身份且只有它可读取交易凭据。

`00001_a1_postgres_baseline.sql` 建立当前业务基线，`00002_a1_observability.sql` 增加审计表，`00003_a2_market_contract.sql` 当前建立两张普通行情表、唯一的 `market_candles` Timescale hypertable 和 `market_flow_leases` 协调表，并配置 7 天 chunk、30 天后 columnstore 压缩和默认 2 年 retention。ADR-0008 已决定在 Binance 行情纵向 PR 中删除不再需要的流租约；该文件仍处于正式 Paper 观察前的整理窗口，重写后必须重建开发/CI 空库。本手册不承诺升级任何旧 SQLite、MySQL 或未投产 PostgreSQL schema。

## Migration 冻结点

- 正式 Paper 观察前属于基线整理窗口。历史 migration 的重写必须来自已记录的设计决策，并同时要求所有开发和 CI 数据库从空库重建。
- 开始记录 Paper 晋级证据前，执行最后一次空库 Up/Down/重放，记录基线版本和证据并永久冻结历史文件。
- 冻结后任何环境都只能追加新 migration，不得修改、重排或复用既有版本号。
- 如果基线整理窗口内已经出现必须保留的数据，应立即提前冻结，不能继续依赖重置。

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
- Paper 晋级证据开始记录并冻结后，已存在的文件禁止修改、重排或复用版本号；冻结前重整必须按上节重建全部非生产数据库。
- 默认保持事务执行。确需非事务 DDL 时必须使用独立 PR，写明失败恢复步骤并完成 PostgreSQL 演练。
- Migration 默认跟随所属纵向能力；共享基线、破坏性或跨领域 schema、凭据、风控和订单状态机使用独立 PR。破坏性删除采用“扩展、回填、切换、收缩”多阶段流程。
- 金融时间使用 UTC `timestamptz`；价格、金额、数量和费率使用 `numeric(38,18)`，不得使用浮点账务列。
- `Down` 必须保护数据。无法无损回滚时明确声明只允许恢复备份，不能提供伪可逆 SQL。

## 自动化验证

```bash
cd backend
COINSPHERE_TEST_POSTGRES_DSN='postgres://coinsphere:test-only@127.0.0.1:5432/coinsphere_test?sslmode=disable' \
  go test -count=1 ./internal/db ./internal/marketdata/... ./internal/migration ./internal/service ./cmd/migrate
```

测试账户必须能创建和删除随机隔离 schema。CI 使用固定 TimescaleDB 镜像并覆盖：

- 空 schema 应用全部内置 migration、重复 Up、空库 Down 和重新 Up。
- `00003` 的 Timescale extension、hypertable、chunk、columnstore/retention policy、租约约束、空事实表 Down/重放；任一行情事实表非空时必须原子保留四张表、数据和版本记录，只有租约行时允许回滚。
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
3. `00003` Down 前停止所有 Collector 和会写入 `market_instruments`、`market_candles`、`market_ticker_snapshots`、`market_flow_leases` 的进程；三张事实表均为空时才会成功，协调租约行会随表丢弃且不阻塞回滚。任一事实表非空或 Down 失败时四张表、数据和版本记录都会保留。
4. 基线 Down 只适用于从未产生任何业务数据的 schema。执行前停止 Backend、Worker 和所有其他写入者；Down 会锁住全部业务表并再次检查为空。
5. 任一表非空或 Down 失败时停止尝试，保留现场并从发布前备份恢复。禁止删除业务行、手工修改 `schema_migrations` 或跳过版本。
6. 恢复后重新执行 `version`、就绪检查和领域数据校验。

部署脚本不会自动执行 Down，也不会自动覆盖 PostgreSQL 数据。旧 SQLite 卷不会被新 Compose 挂载或删除，需要时由管理员在确认不再回滚旧版本后单独清理。
