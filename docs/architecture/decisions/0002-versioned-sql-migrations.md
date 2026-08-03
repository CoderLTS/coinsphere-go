# ADR-0002：采用嵌入式版本化 SQL migration

- 状态：已接受
- 日期：2026-08-01

## 背景

现有服务在启动时通过 GORM `AutoMigrate` 修改 schema。该方式缺少显式版本、可审查 SQL、确定的升级顺序和可演练回滚，不适合作为行情、回测账本、订单与风控状态的生产演进机制。同时，A0 不能在缺少完整存量 schema 基线与恢复演练时直接改变现有应用启动行为。

## 决策

- 使用 Goose v3 Provider 执行 `backend/internal/migration/sql` 中的嵌入式 SQL，版本记录表固定为 `schema_migrations`。
- 镜像同时构建独立 `/app/coinsphere-migrate`，服务进程不会自动调用该命令。
- migration Runner 支持 SQLite 开发态和目标 PostgreSQL；MySQL migration 不属于目标架构或本次验收范围。
- PostgreSQL 操作使用 session advisory lock；一次多步 Down 通过单个 Provider 操作持有整批锁。
- 每个迁移文件默认在独立事务中执行。批次中后续文件失败时，已提交文件保留，失败文件回滚并返回结构化部分结果。
- 数据库版本高于二进制内置版本时，只允许读取版本，禁止 Up、Down 和 Status。
- A0 的 `00001` 是无业务 DDL 的机制基线，A1-2 的 `00002` 独立建立 `worker_tasks`。
- A1-3 的 `00003` 是一个逻辑版本，因 SQLite 与 PostgreSQL 的约束和锁语义不同而提供两个方言文件；Runner 在收集版本前排除另一方言文件，禁止把两个文件当作两个迁移版本执行。
- `00003` 保留既有 Outbox 事件并建立五态、租约 fencing、最大尝试、死信和告警留存契约。其 Down 仅允许空表；非空时必须在同一事务内 fail-closed。代码回滚默认保留 schema 与迁移版本，不以删除事件换取降级。
- 应用仍保留 `AutoMigrate`；检测到 `00003` 字段后，启动路径以同表名占位模型隔离 Outbox DDL，同时保留关系元数据以补齐 PostgreSQL 空库外键，其余显式业务模型和关系继续迁移。SQLite/PostgreSQL 运行时在组装前必须校验 `00003` 已存在，缺失时 fail-fast 并提示先执行独立 migration，服务进程本身不代跑。A1-10 再用独立 PR 完成其余业务 SQL 基线、存量校准、启动切换和恢复演练。

## 结果

- SQL、版本顺序、迁移命令和回滚说明可以随 PR 审查，并由 SQLite 快速测试和 PostgreSQL CI 契约重复验证。
- 发布镜像自带与代码版本一致的 migration，不依赖目标主机动态下载 CLI。
- Goose 会提升部分共享数据库驱动依赖版本，因此本 PR 必须保留应用 `AutoMigrate` 回归测试、全量 Go 测试和无 CGO Linux 构建验证。
- 多文件批次不是总事务。发布流程必须备份、读取版本、记录部分成功结果，并在失败后重新检查状态。
- 已应用的 SQL 文件必须保持不可变；修正通过追加新版本完成。
- SQLite 与 PostgreSQL 必须共同验证空库升级、旧库保数升级、重复 Up、Down 后重放和单文件失败原子性；Outbox 还必须验证五态、租约字段一致性、索引和非空 Down 保护。

## 未采用方案

- 继续只用 GORM `AutoMigrate`：无法提供显式版本、Down 路径和可审查的生产 DDL。
- 运行时自动迁移：服务副本并发启动会把 schema 变更与可用性耦合，且弱化发布前人工门禁。
- 目标主机安装外部 Goose CLI：会引入工具版本漂移和运行时下载，不利于固定镜像与离线回滚。
- 在 A0 直接替换现有 schema：存量基线、数据兼容和恢复证据不足，必须留到 A1 独立迁移 PR。
