# ADR-0002：采用 PostgreSQL 嵌入式版本化 SQL migration

- 状态：已接受
- 日期：2026-08-01
- 修订：2026-08-03，A1 PostgreSQL 基线

## 背景

服务曾同时支持 SQLite、MySQL 和 PostgreSQL，并在启动时通过 GORM `AutoMigrate` 修改 schema。多方言和隐式 DDL 会扩大金融数据约束、并发语义和发布回滚的验证面。CoinSphere 尚未投产，现有开发数据允许重置，因此没有继续维护旧 schema 或旧数据升级路径的必要。

## 决策

- 运行数据库只支持 PostgreSQL/TimescaleDB。Backend 与 Python Worker 使用同一数据库和 schema。
- 使用 Goose v3 Provider 执行 `backend/internal/migration/sql` 中的嵌入式 SQL，版本记录表固定为 `schema_migrations`。
- `00001_a1_postgres_baseline.sql` 是唯一初始基线，在空 schema 中一次建立当前 Go 业务表、`worker_tasks`、外键、索引和状态约束；不兼容或升级旧 SQLite、MySQL、旧 PostgreSQL schema。
- 镜像同时构建独立 `/app/coinsphere-migrate`。服务进程不执行 migration 或 `AutoMigrate`，只以只读查询确认数据库版本与二进制内置最新版本一致。
- migration 使用 PostgreSQL session advisory lock；每个文件默认在事务中执行。多文件批次不是一个总事务，后续版本失败不会撤销此前已提交版本。
- 数据库版本高于二进制内置版本时，旧二进制禁止 Up、Down 和 Status，避免误操作新 schema。
- 基线 Down 会在同一事务中先对全部业务表取得 `ACCESS EXCLUSIVE` 锁，再检查所有表均为空。任一表有数据时回滚失败并保留全部表、数据和版本记录。
- 已在任何共享环境应用的 migration 文件保持不可变；修正只能追加新版本。

## 结果

- 数据库配置只包含 PostgreSQL DSN 和连接池参数，SQLite/MySQL 驱动、双方言 SQL、SQLite 数据卷及测试兼容层全部删除。
- SQL、版本顺序、数据库约束和回滚保护可以随 PR 审查，并在固定 TimescaleDB 镜像上验证。
- 开发环境切换到本基线时直接删除旧开发数据卷并从空库重建，不提供导入或校准工具。
- 代码回滚默认保留当前 PostgreSQL schema 和 migration 版本。只有数据库从未产生任何业务数据、全部写入者已停止时，才允许显式执行基线 Down；否则从经验证的备份恢复，而不是清空数据或修改 `schema_migrations`。

## 未采用方案

- 继续使用 GORM `AutoMigrate`：无法提供显式版本、可审查 SQL 和确定的发布顺序。
- 保留 SQLite/MySQL 兼容：目标运行栈不需要，会让事务和约束测试长期倍增。
- 提供旧数据升级 migration：系统尚未投产，重置开发数据比维护一次性兼容路径更可靠。
- 运行时自动迁移：会把副本启动、DDL 锁和服务可用性耦合。
- 在目标主机安装外部 Goose CLI：会引入工具版本漂移和运行时下载。
