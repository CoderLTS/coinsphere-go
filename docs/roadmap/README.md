# CoinSphere 路线图

## 产品方向

CoinSphere 从通用后台与工作流系统演进为个人币圈量化平台，固定路径为：

`行情采集 -> 数据治理 -> K线研究 -> 新闻因子 -> 策略开发 -> 双回测 -> 内置模拟盘 -> 小额现货 -> USDT永续`

首轮支持 Binance、OKX、现货、USDT 永续逐仓双向持仓。首轮不支持逐笔成交、完整盘口、跨交易所套利、多标的组合、币本位、全仓、高频交易、高级算法单或 AI 自主交易。

完整范围、PR 顺序、阶段门禁和验收标准见 [全流程实施计划](./implementation-plan.md)。

## 当前状态

- 当前阶段：A1 基础稳定化。
- 当前交付：A1-2 Worker PostgreSQL 租约、心跳、崩溃回收与取消运行时。
- 阶段状态：A0 已完成并经用户手工放行，A1 进行中；`worker_tasks` schema 与 Python Worker 运行时均已建立，开发 Compose 和 CI 覆盖原子认领、租约 fencing、过期恢复、最大尝试次数和 5 秒内取消。生产 Release 仍不部署 Worker，下一独立交付为 Outbox 可靠投递。
- GitHub 远程仓库、生产 Runner 和仅允许 `main` 的 `production` Environment 已配置；私有仓库的分支保护受当前 GitHub 套餐限制，操作约束见 [GitHub 治理手册](../runbooks/github-governance.md)。

## 里程碑

| 阶段 | 状态 | 活跃开发时间 | 交付结果 |
| --- | --- | ---: | --- |
| A0 工程系统 | 已完成 | 1-2 周 | GitHub Actions、规则文档、ADR、PR 模板和测试入口 |
| A1 基础稳定化 | 进行中 | 2-3 周 | 取消、Outbox、事务、Auth、WebSocket、迁移和观测 |
| A2 行情底座 | 待开始 | 4-5 周 | TimescaleDB、双所元数据、1m 行情、补数和质量治理 |
| A3 研究数据层 | 待开始 | 3-4 周 | K 线工作台、Parquet 数据集、新闻和 LLM 因子 |
| A4 策略与向量回测 | 待开始 | 4-5 周 | Monaco、多文件版本、沙箱 Worker 和 vectorbt |
| A5 事件回测 | 待开始 | 5-6 周 | NautilusTrader、合约模型和双引擎比较 |
| A6 内置模拟盘 | 待开始 | 4-5 周 | 虚拟账户、实时撮合、风控和对账 |
| A7 小额现货 | 待开始 | 4-5 周 | 双所现货、幂等下单、恢复和急停 |
| A8 永续合约 | 待开始 | 5-7 周 | USDT 永续双向仓、条件单和清算风险 |

## 阶段门禁

- 研究版本以可复现的 vectorbt 与 NautilusTrader 双回测为退出条件。
- 模拟盘连续运行 30 天且至少完成 100 个订单、无未解释账务差异，才能申请现货放行。
- 小额现货连续稳定 30 天后才能申请永续放行。
- 阶段晋级必须由用户审查报告并手工确认。
