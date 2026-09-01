# 数据库迁移

## 当前基线

CoinSphere 只支持 PostgreSQL 16。`00001` 至 `00005` 创建认证、插件、工作流和运行基线；`00006_quant_market_backtests.sql` 首次创建 Binance 行情表与 Quant 回测表；`00007_paper_results_notifications.sql` 创建 ResultView、Quant Signal 与 Notification 投递；`00008_quant_instrument_sources.sql` 创建 Binance 品种来源；`00009` 至 `00016` 完成日志、通知、工作流和回测明细基线；`00017_quant_binance_split.sql` 创建 Binance 订单、成交、费用、Paper 账本、持仓和账户快照约束。

服务启动只读校验核心版本，DDL 只由 `coinsphere-migrate` 执行。随应用发布的 Quant、Binance 与 Notification 基线使用核心 migration runner；通过插件 CLI 安装的插件使用 `plugin_<规范化插件 ID>` schema 和自己的 `schema_migrations` 账本。项目不提供旧表、旧接口或旧数据转换器；生产 DSN 和数据库密码只通过服务器配置注入。

## 命令

在 `backend` 目录执行：

```bash
go run ./cmd/migrate -config ./config.yml -direction status
go run ./cmd/migrate -config ./config.yml -direction version
go run ./cmd/migrate -config ./config.yml -direction up
go run ./cmd/migrate -config ./config.yml -direction down -steps 1
```

镜像内命令为 `/app/coinsphere-migrate`。`up` 可重复执行；数据库版本落后或领先当前二进制时，应用拒绝启动。开发 Compose 由一次性 `migrate` 服务先建 schema，再启动 Backend。

## 退出 TimescaleDB

旧独立数据库从 PostgreSQL 17/TimescaleDB 迁入服务器 PostgreSQL 16 时，必须在发布维护窗口执行一次逻辑迁移：

1. 停止 CoinSphere 应用容器，并保存旧数据库和 `data/backend` 的一致恢复点。
2. 在旧数据库中把历史 K 线 hypertable 无损转换为与 `plugin_binance.candles` 约束和索引一致的普通表，再卸载 TimescaleDB 扩展。
3. 在共享 PostgreSQL 中创建独立 `coinsphere_go` 用户和空数据库，不覆盖已有 `coinsphere` 旧库或其他应用数据库。
4. 使用 PostgreSQL 17 客户端逻辑导出并恢复到 PostgreSQL 16；先在隔离目标验证版本兼容，再切换生产连接。
5. 对比 migration 版本、schema、关键表行数和数据库大小，运行目标应用镜像健康检查。
6. 保留旧数据库目录作为人工回滚备份；只有共享数据库备份完成恢复演练后才清理。

任何一步失败都停止候选服务并恢复旧 Compose，不删除旧数据库目录，也不修改其他共享数据库。

## 当前基线重置

此流程会永久删除当前 CoinSphere 数据库内容，必须同时满足：

1. 已确认数据库没有需要保留的 Paper 晋级证据或其他业务数据。
2. 已创建并验证可恢复的数据库备份。
3. 用户对目标环境和目标 CoinSphere 数据目录给出明确重置授权。
4. 已只读确认目标 Compose 项目、数据库服务和数据目录，不影响共享服务。

满足条件后，停止 CoinSphere 应用容器，备份并定向重建共享 PostgreSQL 中的 `coinsphere_go` 数据库，清空该部署独占的 `data/backend/artifacts` 目录，再由目标镜像执行 `coinsphere-migrate -direction up`。删除前必须分别解析并核对数据库名和制品绝对路径；禁止手工改写 `schema_migrations` 来伪装重置，也不得触及已有 `coinsphere` 旧库、其他应用数据库、上传目录或真实交易凭据。

## 变更规则

- 开始正式 Paper 观察前必须在 GitHub 验收记录或发布记录中标记 migration freeze 提交；从该提交开始，核心与内置插件 migration 只追加递增版本，已有文件必须保持字节不变。
- 每个 migration 包含 `-- +goose Up` 和 `-- +goose Down`，默认在事务中执行。
- 插件 migration 使用独立递增版本；兼容升级只能追加高版本，卸载和应用回滚不执行插件 Down。
- 金融时间使用 `TIMESTAMPTZ`；价格、数量、金额和费率使用 `NUMERIC(38,18)`。
- `Down` 必须保护持久数据；插件和工作流 migration 只在所属表为空时允许回滚。
- `00005` 只有在 Run、RunNode、节点日志、检查点、事件、人工任务、状态和制品全部为空时允许 Down。
- `00006` 只有在 Binance 品种/K 线和 Quant 回测摘要均为空时允许 Down；应用回滚不得自动删除插件 schema。
- `00007` 只有在 ResultView、Quant Signal 和 Notification 投递全部为空时允许 Down；存在任一记录时必须恢复备份，不能删除事实来迁就旧应用。
- `00008` 只有在所有 Binance 品种来源为空时允许 Down；`00009` 只有在系统日志和日志设置都为空时允许 Down。
- `00010` 只有在不存在外部渠道、站内收件人、已读或错误类别数据时允许 Down；应用回滚不执行该 Down，也不删除通知记录。
- `00011` 的生产预检清单固定为旧工作流 ID `3`、活动修订 `7` 和图哈希 `ceb29098af6afa49b7a20d9698f53fa7`。发布前旧服务必须已停止并完成数据库备份；清单、活动图、金融事实、共享制品或跨工作流诊断引用任一不符都会终止事务。该 migration 删除旧 Run、通知、日志、检查点、状态、修订和工作流后创建新 ID；Down 永远拒绝，只能恢复发布前备份。
- 无法无损回滚时恢复已验证备份，不提供伪可逆 SQL。
- 禁止应用启动自动建表、手工修改 migration 账本或用删除业务行修复版本差异。

## 验证

数据库变更在隔离环境通过 migration runner、容器启动和恢复演练验收。最小范围是空库 Up、重复 Up、关键约束与 K 线索引、数据库超前拒绝、非空库保护和 Paper 账本重建；不得连接生产数据库执行验证。恢复演练见 [Paper 恢复与观察](paper-recovery.md)。

## 发布与回滚

本基线不能对旧 version 4 数据库执行原地 Up。部署前必须按上面的授权流程重置 CoinSphere 自有数据库；旧账本与当前迁移文件不一致时，migration runner 也会在缺失的当前表上停止。

应用回滚不自动执行 Down。若需要回滚到重置前版本，停止当前应用并恢复重置前已验证备份及与其匹配的应用镜像。不得把旧应用指向当前基线，也不得把当前应用指向旧 schema。
