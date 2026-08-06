# ADR-0008：自用优先的单实例拓扑

- 状态：接受
- 日期：2026-08-05
- 替代：ADR-0001

## 背景

CoinSphere 的实际目标是一台 Linux 主机上的个人自用系统，额外用户数量有限。ADR-0001 为尚未出现的扩容需求设计了 API、Collector、Scheduler 和 Executor 多角色、多实例、流租约与跨实例通知，增加了部署、连接预算和恢复路径，却没有改善当前核心闭环。

## 决策

- 保持单仓库和模块化单体，但生产拓扑固定为 Web、单实例 Go App、单实例 Python Worker、单实例 Go Executor 和 PostgreSQL/TimescaleDB。
- Go App 合并 API、认证/RBAC、工作流、调度、公共行情采集、信号协调和通知，不再使用 `COINSPHERE_ROLE` 拆分 API、Collector 或 Scheduler。
- Executor 从 Paper 阶段开始部署，是唯一调用 Binance 私有接口的组件。Go App、Worker、工作流和通用 HTTP 节点不得调用私有接口。
- 不建设多实例协调。行情流不使用 PostgreSQL 租约；`market_flow_leases` 和 Runner 已在 Binance 行情纵向能力中删除。
- PostgreSQL 继续承担事实存储、任务队列和 Outbox。任务租约与 Outbox 租约用于崩溃恢复和事务投递，保留它们不代表支持多实例扩容。
- 通知记录以数据库为事实源，单 Go App 在事务提交后直接唤醒本进程 WebSocket Hub；不增加跨 API 实例 `pg_notify` 中继。
- 不引入 Redis、Kafka、NATS、Consul、PgBouncer、Kubernetes 或服务发现。

## 结果

- 部署和故障面与自用规模匹配，公共行情、工作流和 API 仍由清晰模块边界隔离。
- Go App 故障会同时影响 API、行情和调度；这是单机自用阶段接受的上限，先通过进程重启、幂等写入和补数恢复处理。
- 只有单进程达到已测量的 CPU、内存、连接或可用性上限后，才能新增 ADR 评估拆分或多实例。

## 未采用方案

- 保留四角色单镜像：仍需维护角色装配、部署矩阵和分布式协调。
- 拆成微服务或加入消息中间件：当前没有独立扩缩容、团队边界或吞吐证据。
