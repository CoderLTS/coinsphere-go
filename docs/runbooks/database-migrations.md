# 数据库迁移

## 当前基线

CoinSphere 只支持 PostgreSQL/TimescaleDB。正式 Paper 观察前，历史 migration 已重置为单一 `backend/internal/migration/sql/00001_initial.sql`；所有环境必须从空库建立版本 1，不提供旧 schema 或旧数据升级路径。

服务启动只读校验版本，DDL 只由 `coinsphere-migrate` 执行。生产 DSN 和数据库密码只通过服务器配置注入。

## 命令

在 `backend` 目录执行：

```bash
go run ./cmd/migrate -config ./config.yml -direction status
go run ./cmd/migrate -config ./config.yml -direction version
go run ./cmd/migrate -config ./config.yml -direction up
go run ./cmd/migrate -config ./config.yml -direction down -steps 1
```

镜像内命令为 `/app/coinsphere-migrate`。`up` 可重复执行；数据库版本领先二进制时拒绝运行。开发 Compose 由一次性 `migrate` 服务先建 schema，再启动 Backend 和 Worker。

## 变更规则

- 当前初始化重置完成后，后续 schema 变更从 `00002_*.sql` 开始追加，不再改写 `00001_initial.sql`。
- 每个 migration 包含 `-- +goose Up` 和 `-- +goose Down`，默认在事务中执行。
- 金融时间使用 `TIMESTAMPTZ`，价格、数量、金额和费率使用 `NUMERIC(38,18)`。
- `Down` 必须保护持久数据；无法无损回滚时依赖备份，不提供伪可逆 SQL。
- 禁止应用启动自动建表、手工修改 `schema_migrations` 或用删除业务数据修复版本差异。

## 验证

迁移变更至少执行：

```bash
cd backend
COINSPHERE_TEST_POSTGRES_DSN='postgres://coinsphere:test-only@127.0.0.1:5432/coinsphere_test?sslmode=disable' \
  go test -count=1 ./internal/migration ./internal/db ./internal/service ./cmd/migrate
```

最小验收范围是空库 Up、重复 Up、空库 Down、重新 Up、关键约束/索引、Timescale 生命周期和非空库 Down 保护。

## 发布与回滚

生产独立 Compose 首次创建新的 TimescaleDB 卷并执行版本 1，不改写旧共享数据库。后续发布先完成备份，再由目标 Backend 镜像执行 Up。应用回滚保留当前 schema，不自动执行 Down；需要恢复数据库时只使用已验证备份。
