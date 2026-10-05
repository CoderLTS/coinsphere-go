# 数据库迁移与工作流迁移

CoinSphere 只支持 PostgreSQL 16。当前为 generation 4：Core 使用 public.schema_migrations_g4，官方业务和外部插件各自使用专属 schema_migrations_g4。旧 00001–00023 SQL 保留且不改写；新 runner 发现旧 public.schema_migrations 时拒绝原地 Up。

## 新基线

Core 基线位于 backend/internal/migration/generation4，领域基线位于 backend/plugin/official/{quant,binance,notification}/migrations。`/app/coinsphere-migrate -config /server/target.yml -direction up` 建立 Core 和官方基线；可重复执行。应用启动只验证版本，不执行 DDL。

Core 及已启用插件各自校验自己的账本；外部插件的最新 migration 版本由源码安装时写入生成目录。缺账本、版本超前/落后、错误 generation 或 PostgreSQL 主版本均拒绝启动。generation 4 Down 明确拒绝，回退用匹配恢复点。开始正式 Paper 观察后必须冻结已有 migration，随后只追加版本。

## 迁移范围

一次性 `/app/workflow-migrate` 将旧账本版本 23 的当前工作流定义迁入**独立、空的 generation 4 目标数据库**。源只有只读访问，目标应用在导入前不得启动，避免种子和运行数据占用空目标。

保留工作流/当前修订/节点 ID、必要用户角色和分组、模型/代理配置、已知非交易必要凭据、ResultView 固定范围及主体授权。图转 Graph 3，legacy realtime 入口转 main，其他入口保留；Loop Secret 使用实际展开 ID。旧通知组合变为插件节点；同名输出 CEL 不猜来源，要求明确表达式或字段来源。

所有导入工作流、视图、模型及代理保持 inactive/disabled。运行、待办、事件、检查点、状态、通知、账本、行情及旧 Live 放行不迁移；必要行情需补齐。交易与未知插件凭据仅盘点存在性，源密文不读取到工具进程，必须在服务器重新绑定。工具不发外部请求或执行交易。

旧菜单上无效的管理按钮不转为新能力；受保护、工作流/插件执行、凭据和资源管理授权须在目标明确重新配置。缺必要 Secret、inactive owner 等作为启用依赖报告；未知版本、缺定义、CEL 来源不明、结果节点归属或授权主体缺失作为阻断项。

## 维护窗口与恢复点

1. 记录旧/候选 commit、镜像 digest、配置版本、PostgreSQL 主版本、数据库标识、插件清单与 Paper freeze/观察状态。报告不含凭据值、真实 DSN、用户名或原始图。
2. 在服务器保存旧数据库、配对配置/加密 key 和独占上传/制品的一致恢复点；在隔离副本验证能恢复旧镜像。恢复凭据只由服务器注入，不给 Codex/CI。
3. 创建独立 generation 4 目标和候选独占文件目录，使用候选镜像建立基线。安装/编译所有需要的 SDK 4 外部插件，确认版本精确一致和依赖完整；不要启动候选应用。
4. 用 inspect 盘点源资产，建立映射并完成 plan。源还在写入时的 plan 仅供预审；最终维护窗口停止旧应用、Trigger、所有写入者后重新 inspect/plan。
5. 最终 plan 必须无 issues；每条依赖都有服务器处理记录或明确 inactive 保留。apply 使用 source-stopped 声明，目标仍不运行。verify 完成后保存无密钥报告与完整 planHash。
6. 先启动受限候选应用，核对工作流/修订/节点数量及图语义、Secret 配置状态、角色能力/资源范围和空执行表；按已批准范围补凭据/行情、发布定义再启用。首轮只使用合成/通用/Paper 路径；Live 状态单独手工放行。
7. 通过后切换连接/入口，保存新的恢复点。任何阻断、指纹变化或恢复不完整都保持候选关闭，不用手工改账本或删事实绕过。

## 命令

以下在服务器或隔离环境执行，配置文件路径仅示意。源 DSN 与旧非交易加密 key 由权限受限环境注入 `COINSPHERE_MIGRATION_SOURCE_DSN` / `COINSPHERE_MIGRATION_SOURCE_KEY`；不在参数、终端日志或报告中展开。目标连接/key 从 target.yml 或既有环境配置读取。

```bash
/app/workflow-migrate -command inspect -source-id instance-a -output /server/inspect.json
/app/workflow-migrate -command plan -source-id instance-a \
  -target-config /server/target.yml -mappings /server/mappings.json \
  -plan /server/plan.json -output /server/plan-report.json
/app/workflow-migrate -command apply -source-id instance-a -source-stopped \
  -target-config /server/target.yml -plan /server/plan.json -output /server/apply-report.json
/app/workflow-migrate -command verify -target-config /server/target.yml \
  -plan /server/plan.json -output /server/verify-report.json
```

inspect 不连接目标。plan 保存绑定源身份/内容指纹、目标 database_id、编译目录和映射的哈希；issues 非空仍保存计划但返回失败，apply 被拒绝。apply 重新生成并比较完整计划，用单一 Serializable 事务导入，失败全部回滚。相同批次重跑先验证资产再返回 alreadyApplied，不重复建资产。verify 只读且需在目标开始人工修改之前执行；修改后验证失败是预期保护。

映射及去秘密报告样例见 [migration examples](../examples/workflow-migration-mappings.json) 与[报告](../examples/workflow-migration-report.json)。样例 ID 仅为合成值；Hash 样例不能用于 apply。

## 回退

- apply 事务失败：目标无部分资产；保留计划、受控错误与源，修正映射后重新 plan。源内容变化必须生成新计划，不编辑 plan 的 hash。
- 候选启用前失败：停止候选，旧镜像继续指向未改写源库及原配置/文件；目标隔离保留供调查。
- 候选启用后失败：停止候选所有写入者，记录已产生的外部副作用，恢复旧数据库/镜像/配置/文件组合并切回入口。禁止把旧镜像指向新库或反向混用。
- 有通知或外部调用时，先由业务人员核对去重/已发送结果，不自动重放。若涉及交易，先停用账户放行并手工核对订单/成交/持仓，恢复应用不会自动撤单或交易。
- 不执行 schema Down、手工修改版本表、覆盖源库或清理共享服务。恢复点无法验证时保持服务停止。

## 验收证据

远端 PostgreSQL 16 CI 覆盖合成旧库导入、图转换、事务失败回滚、幂等、指纹冲突、作用域与必要凭据阻断、插件版本不符阻断，以及用 PostgreSQL 16 pg_dump/pg_restore 恢复到独立旧库并核对 schema/定义/配置事实。导入后同时核对源指纹未变化；所有网络替身不接触生产。真实源库盘点、外部插件转换、服务器非交易秘密重绑和匹配镜像/数据库/配置/文件恢复演练必须在维护窗口记录，不能宣称 CI 已替代现场证据。
