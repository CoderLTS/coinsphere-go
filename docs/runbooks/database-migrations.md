# 数据库迁移

## 当前基线

CoinSphere 只支持 PostgreSQL/TimescaleDB。`backend/internal/migration/sql/00001_initial.sql` 是正式 Paper 观察前重建的 V2 空库基线；它只创建认证、RBAC、菜单、i18n、审计所需表和 TimescaleDB 扩展，不创建行情 hypertable。

服务启动只读校验版本，DDL 只由 `coinsphere-migrate` 执行。项目不提供旧表、旧接口或旧数据转换器；生产 DSN 和数据库密码只通过服务器配置注入。

## 命令

在 `backend` 目录执行：

```bash
go run ./cmd/migrate -config ./config.yml -direction status
go run ./cmd/migrate -config ./config.yml -direction version
go run ./cmd/migrate -config ./config.yml -direction up
go run ./cmd/migrate -config ./config.yml -direction down -steps 1
```

镜像内命令为 `/app/coinsphere-migrate`。`up` 可重复执行；数据库版本落后或领先当前二进制时，应用拒绝启动。开发 Compose 由一次性 `migrate` 服务先建 schema，再启动 Backend。

## V2 基线重置

此流程会永久删除当前 CoinSphere 数据库内容，必须同时满足：

1. 已确认数据库没有需要保留的 Paper 晋级证据或其他业务数据。
2. 已创建并验证可恢复的数据库备份。
3. 用户对目标环境和目标 CoinSphere 数据卷给出明确重置授权。
4. 已只读确认目标 Compose 项目、数据库服务和数据卷，不影响共享服务。

满足条件后，停止 CoinSphere Backend/Web，定向重建 CoinSphere 自有数据库或数据卷，再由目标镜像执行 `coinsphere-migrate -direction up`。禁止手工改写 `schema_migrations` 来伪装重置。

## 变更规则

- P0 基线确认部署后，后续核心 schema 从 `00002_*.sql` 开始追加，不再改写 `00001_initial.sql`。
- 每个 migration 包含 `-- +goose Up` 和 `-- +goose Down`，默认在事务中执行。
- 金融时间使用 `TIMESTAMPTZ`；价格、数量、金额和费率使用 `NUMERIC(38,18)`。
- `Down` 必须保护持久数据；V2 基线只允许在九张业务表全部为空时回滚。
- 无法无损回滚时恢复已验证备份，不提供伪可逆 SQL。
- 禁止应用启动自动建表、手工修改 migration 账本或用删除业务行修复版本差异。

## 验证

```bash
cd backend
COINSPHERE_TEST_POSTGRES_DSN='postgres://coinsphere:test-only@127.0.0.1:5432/coinsphere_test?sslmode=disable' \
  go test -count=1 ./internal/migration ./internal/db ./internal/service ./cmd/migrate
```

最小验收范围是空库 Up、重复 Up、空库 Down、重新 Up、精确表集合、关键约束/索引、TimescaleDB 扩展且无 hypertable、数据库超前拒绝和非空库 Down 保护。

## 发布与回滚

本基线不能对旧 version 4 数据库执行原地 Up。部署前必须按上面的授权流程重置 CoinSphere 自有数据库；否则 migration runner 会因数据库领先二进制而停止。

应用回滚不自动执行 Down。若需要回滚到重置前版本，停止当前应用并恢复重置前已验证备份及与其匹配的应用镜像。不得把旧应用指向 V2 基线，也不得把 V2 应用指向旧 schema。
