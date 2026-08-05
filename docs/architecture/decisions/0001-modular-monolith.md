# ADR-0001：模块化单体、单镜像与多角色运行

- 状态：已被 ADR-0008 替代
- 日期：2026-08-01
- 修订：2026-08-04，冻结角色、扩容和通知边界
- 替代日期：2026-08-05

## 背景

CoinSphere 需要持续行情采集、粗粒度调度、隔离回测、模拟盘和实盘执行，但部署目标仍是个人 Linux 主机。拆成微服务会提前引入网络契约、发布协调和额外故障面；继续把所有职责放在一个进程又会让采集、调度和执行互相影响。

## 决策

保持单仓库、模块化单体、单 Go 二进制和单应用镜像。目标角色模型固定为 `COINSPHERE_ROLE=api|collector|scheduler|executor`；各阶段只接受已实现的角色值，缺失或非法值拒绝启动，不提供隐式 `all`。

- A2.1 只实现并常驻 `api`、`collector`、`scheduler`；`executor` 连同交易凭据能力从 A6 开始实现和部署，不提前创建空壳入口。
- `api` 可多实例；`collector` 按数据流 PostgreSQL 租约分片。
- `scheduler` 使用 session advisory lock 单活生成 cron 触发，所有实例通过行租约认领已有任务与 Outbox。
- `executor` 按交易账户独占租约并使用 fencing，禁止两个 Owner 同时执行同一账户。
- 通知记录以数据库为事实源，`pg_notify` 只唤醒所有 API 实例；离线恢复依赖未读快照。
- 普通角色与 Worker 首期共用受限应用数据库身份，Executor 和 Migrator 独立。普通身份不得读取交易凭据，长期进程不持有 DDL 权限。
- 各角色使用驱动原生连接池，部署按最大实例数核算总预算并预留 migration 和运维连接；首期不引入 PgBouncer。

Python Worker Launcher 是唯一独立语言和容器执行边界。任务队列、租约、Outbox 和通知唤醒均复用 PostgreSQL，不引入 Redis、Kafka、NATS、Consul 或 Kubernetes。

## 结果

- 构建、版本、配置解析和依赖装配只维护一套，采集、调度、API 和执行获得进程级故障隔离。
- PostgreSQL 同时承担事实存储和轻量协调；达到可量化的吞吐或连接上限后再评估专用基础设施。
- 新金融领域不得依赖旧 API 或 service 层。当前依赖方向由 ADR、任务卡和最终只读复审约束，不增加第三方架构测试工具。
