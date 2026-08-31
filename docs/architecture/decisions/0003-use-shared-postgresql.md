# ADR-0003：复用服务器 PostgreSQL

- 状态：已接受（Accepted）
- 日期：2026-08-27
- 决策所有者：CoinSphere
- 替代范围：ADR-0002 中 PostgreSQL/TimescaleDB 的部署决策

## 背景

CoinSphere 是个人自用的单实例系统。当前只有 `plugin_quant.candles` 被转换为 hypertable，查询仍是普通的品种、周期和时间范围索引查询，没有使用压缩、保留策略、时间聚合或 chunk 管理。服务器已经运行 PostgreSQL 16，继续维护 CoinSphere 独立 TimescaleDB 容器没有对应收益。

## 决策

- 生产 Backend 通过外部 `dpanel_stack` 网络连接服务器现有 PostgreSQL 16。
- CoinSphere 使用独立 `coinsphere_go` 数据库和用户，不复用已有 `coinsphere` 旧库，也不访问其他应用数据库。
- 生产 Compose 只运行内置 Vue 静态产物的 Go App；migration 仍由同一目标镜像在启动前执行。
- Quant K 线使用普通表以及 `(market, instrument, interval, open_time DESC)` 联合索引。
- 本地 Compose 自带普通 PostgreSQL 16，避免依赖服务器环境。

## 结果

生产少维护一个数据库容器、镜像和数据目录，备份归入服务器 PostgreSQL 数据栈。代价是 CoinSphere 部署依赖现有 PostgreSQL 和外部 Docker 网络；只有数据规模或查询证明确需压缩、保留策略或时间聚合时，才重新评估时序扩展。
