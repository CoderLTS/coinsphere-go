# ADR-0001：工作流核心化模块单体

- 状态：已接受
- 日期：2026-08-18

## 决策

CoinSphere 采用 `Web + Go App + Python Worker + PostgreSQL/TimescaleDB` 的模块化单体架构，并以工作流作为用户可见的统一编排入口。

- Go App 承担 API、RBAC、工作流、调度、公共行情、通知和 Paper 执行。
- Python Worker 单进程保留 realtime/backtest 两个独立槽位。
- PostgreSQL 同时保存业务事实、工作流状态、任务队列和 Outbox；不增加消息中间件。
- K 线逐条写入、策略计算和下单仍由所属模块执行，工作流只负责粗粒度触发与编排。
- Testnet/Live Private Executor 保留为默认关闭的 `private` profile，并且是唯一可访问 Binance 私有接口的组件。
- 生产使用 CoinSphere 独立 Compose 和独立 TimescaleDB，不与其他应用共享 Compose 项目。

## 结果

默认部署只有 Web、Go App、Python Worker 和 TimescaleDB 四个常驻服务。用户可在同一画布连接定时、手工、事件、行情、策略和通知节点，并从执行记录查看节点状态与错误。系统不提供任意后端插件 SDK；自定义策略节点来自已发布的策略实例。

当前完整设计以[架构概览](../overview.md)为准。
