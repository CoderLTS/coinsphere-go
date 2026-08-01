# 发布与回滚

Codex 只生成镜像、校验和、迁移命令、备份命令和回滚说明，不连接目标主机。

## 当前与目标状态

A0 当前 Compose 仍使用 SQLite，且尚未提供 `research`、`paper`、`live` Profile，只能作为现有管理平台的开发/研究环境。版本化 migration 命令已经可用，但应用启动仍保留 GORM `AutoMigrate`；A1 完成独立切换前，不得把 migration 版本等同于完整业务 schema。后续 PostgreSQL 和 Profile 流程描述的是目标态，相关能力未落地并通过门禁前，不得据此启用模拟盘或实盘。

## 发布前

1. 确认目标 Commit 的 CI、代码审查和安全检查全部通过。
2. A0 备份 SQLite 数据卷与部署配置；迁移到目标架构后，备份 PostgreSQL、策略版本和数据集 Manifest。
3. 按[数据库迁移手册](./database-migrations.md)先检查版本和状态，在研究环境备份后执行 `up` 与健康检查。
4. Profile 落地后，对 `paper` 或 `live` 的启用进行单独人工确认。

## 发布

1. 用户在家用 Linux 主机拉取固定版本镜像，禁止使用 `latest`。
2. A0 启动当前 Compose；目标态先执行 `/app/coinsphere-migrate -direction up`，再启动对应 Compose Profile。
3. 检查就绪状态、任务积压、行情新鲜度、对账和风控状态。

## 回滚

1. 禁止新任务和新增交易敞口，必要时执行“撤单并暂停”。
2. 在当前迁移二进制仍可用时处理数据库：仅在迁移说明明确支持且已演练时执行 Down，否则从备份恢复；禁止直接删除 `schema_migrations` 记录伪造回滚。
3. 数据库恢复并校验后，再恢复上一个固定镜像版本。
4. 记录故障时间线、受影响资源和恢复校验结果。
