# 数据库迁移与回滚

## 边界

CoinSphere 只支持 PostgreSQL/TimescaleDB。DDL 由独立 Migrator 身份和命令执行；服务启动只读校验版本，不执行 `AutoMigrate`。生产 DSN 只通过主机上的 Secret 注入，不进入仓库、Issue、PR、CI、日志或截图。

正式 Paper 观察前仍可整理未投产基线。开始记录 Paper 晋级证据前，必须完成最后一次空库 Up/Down/重放并冻结历史；冻结后只能追加版本，不能修改、重排或复用旧版本号。

## 命令

在 `backend` 目录执行：

```bash
go run ./cmd/migrate -config ./config.yml -direction status
go run ./cmd/migrate -config ./config.yml -direction version
go run ./cmd/migrate -config ./config.yml -direction up
go run ./cmd/migrate -config ./config.yml -direction up -target 1
go run ./cmd/migrate -config ./config.yml -direction down -steps 1
```

可用 `-timeout 10m` 调整默认五分钟超时；Ctrl+C/SIGTERM 会取消操作。版本领先当前二进制时，`up`、`down` 和 `status` 拒绝执行；`version` 仍可读取差异。镜像中的命令为 `/app/coinsphere-migrate`。

开发 Compose 先由一次性 `migrate` 服务应用 Up，再启动应用：

```bash
COINSPHERE_AUTH__SECRET_KEY=local-migration-only docker compose up -d --build
docker compose run --rm migrate /app/coinsphere-migrate -config /app/config.yml -direction status
```

每个 migration 文件必须包含 `-- +goose Up` 和 `-- +goose Down`，默认在事务中执行。确需非事务 DDL 时，必须单独说明失败恢复步骤并在 PostgreSQL 演练。

## 编写约束

- 文件位于 `backend/internal/migration/sql`，使用五位递增版本号。
- 金融时间使用 UTC `timestamptz`；价格、数量、金额和费率使用 `numeric(38,18)`，禁止浮点账务列。
- Migration 默认随所属纵向能力交付；共享基线、破坏性或跨领域 schema、凭据、风控和订单状态机变更可独立交付，并写明回滚方案。
- `Down` 必须保护数据。无法无损回滚时声明只能恢复备份，不提供伪可逆 SQL。

## 验证

使用仅供测试的 DSN 和随机隔离 schema 运行相关测试：

```bash
cd backend
COINSPHERE_TEST_POSTGRES_DSN='postgres://coinsphere:test-only@127.0.0.1:5432/coinsphere_test?sslmode=disable' \
  go test -count=1 ./internal/db ./internal/migration ./internal/service ./cmd/migrate
```

迁移变更至少验证：空 schema Up、重复执行、空库 Down、重新 Up、服务版本不匹配 fail-fast、约束与索引、失败原子性和并发写入保护。测试不得清空固定外部表。

## 发布与回滚

1. 停止会写入受影响表的服务；涉及交易时先禁止新增敞口。
2. 记录固定镜像和 Commit SHA，创建并验证 PostgreSQL 备份。
3. 执行 `version`/`status`，再用目标镜像的 Migrator 执行 Up。
4. 启动服务并检查就绪状态和关键约束。

代码回滚与 schema 回滚分开处理：代码或健康检查失败时恢复上一固定镜像并保留当前 schema 和 `schema_migrations`。只有明确可逆、已在备份副本演练且所有写入者停止时才执行固定步数 Down；其他情况从已验证备份恢复。禁止删除业务行、手工改版本表或让部署脚本自动执行 Down。
