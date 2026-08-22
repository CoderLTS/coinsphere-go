# ADR-0001：工作流核心化模块单体

- 状态：已接受
- 日期：2026-08-18

## 决策

CoinSphere 采用 `Web + Go App + Python Worker + PostgreSQL/TimescaleDB` 的模块化单体架构，并以工作流作为用户可见的统一编排入口。

- `/home` 保留为运行概览，业务任务统一从 `/workbench` 进入；不维护旧业务导航与工作台两套操作界面。
- 工作流使用 Graph V2 的 `flow`/`data` 双边模型和 JSON Schema 端口，界面生成数据映射，不要求用户填写内部状态路径。
- 工作流定义、运行态、执行和等待均保存 Owner，所有用户查询强制按 Owner 过滤；内置定义只作为不可执行模板。
- Worker 任务和高风险人工动作通过数据库等待记录暂停与恢复执行；取消、整次重跑、幂等决策继续复用现有 PostgreSQL 队列和事务边界。
- Go App 承担 API、RBAC、工作流、调度、公共行情、通知和 Paper 执行。
- Python Worker 单进程保留 realtime/backtest 两个独立槽位。
- PostgreSQL 同时保存业务事实、工作流状态、任务队列和 Outbox；不增加消息中间件。
- K 线逐条写入、策略计算和下单仍由所属模块执行，工作流只负责粗粒度触发与编排。
- 策略只保存可发布的源码定义；市场、币种、周期、环境、账户和风控绑定由工作流策略节点持有，激活和停用工作流时事务化创建、复用或关闭策略实例及 K 线订阅。
- 用户节点模板只引用现有内置执行器，不引入任意插件、进程或额外网络权限。
- Testnet/Live Private Executor 保留为默认关闭的 `private` profile，并且是唯一可访问 Binance 私有接口的组件。
- 生产使用 CoinSphere 独立 Compose 和独立 TimescaleDB，不与其他应用共享 Compose 项目。

## 结果

默认部署只有 Web、Go App、Python Worker 和 TimescaleDB 四个常驻服务。用户可在同一工作台连接、配置、运行、审批和排障，并从画布覆盖层与时间线查看节点状态、实际边流转、重试和结构化错误。系统不提供任意后端插件 SDK；工作流激活负责把已发布策略定义变成受风控约束的运行实例。

当前完整设计以[架构概览](../overview.md)为准。
