# CoinSphere 公共契约

本文只冻结当前已实现的跨模块语义。目标能力以[工作流平台 V2](../architecture/workflow-platform-v2.md)和[路线图](../roadmap/README.md)为准；未实现路由不得视为可用接口。

## 通用边界

- 领域时间统一使用 UTC。数据库使用 `TIMESTAMPTZ`，HTTP 和事件使用带时区的 RFC 3339。
- 金融价格、数量、金额、费率和盈亏使用 Decimal；数据库使用 `NUMERIC(38,18)`，JSON 使用十进制字符串。
- 外部枚举、时间、ID、Decimal、URL、文件和凭据在信任边界校验；模块不变量由拥有模块或数据库约束保证。
- 错误响应使用 `application/problem+json`，包含 `type`、`title`、`status`、`detail` 和 `requestId`。

## 身份与 HTTP

- 不提供公开注册。`POST /api/v1/auth/login` 是唯一匿名身份 API。
- `POST /api/v1/auth/logout`、`POST /api/v1/auth/reauth` 和 `GET /api/v1/me` 要求有效 Access Token。
- `POST /api/v1/auth/reauth` 返回绑定当前用户与当前会话、五分钟失效且只能使用一次的不透明 Token。
- 当前业务 API 包含 `/api/v1/home/*`、`/api/v1/admin/users`、`/api/v1/system/*`、工作流路由、ResultView 路由和系统插件路由；除匿名 Webhook 和获授权 ResultView 外，工作流与系统插件路由只允许 `R_SUPER`。
- `/health/live` 只报告进程存活；`/health/ready` 和 `/health` 在一秒预算内检查 PostgreSQL；`/metrics` 要求登录。
- 旧行情、策略、回测、信号、通知和交易路由已移除，不提供别名或兼容响应。

## 工作流 P1-P4

| 路由                                                            | 语义                                   |
| --------------------------------------------------------------- | -------------------------------------- |
| `GET /api/v1/workflows/templates`                               | 列出当前可创建的批次、事件和连续流模板 |
| `POST /api/v1/events`                                           | 发布 CloudEvents 1.0 结构化 JSON       |
| `POST /api/v1/webhooks/{workflowId}`                            | 通过工作流 Secret 发布 Webhook 事件    |
| `GET /api/v1/human-tasks`                                       | 查询待处理人工任务                     |
| `POST /api/v1/human-tasks/{taskId}`                             | 一次性批准或拒绝人工任务               |
| `GET /api/v1/workflows/node-definitions`                        | 列出核心与编译期插件节点 Schema        |
| `POST /api/v1/workflows/validate`                               | 只读校验完整工作流图                   |
| `GET/POST /api/v1/workflows`                                    | 列表，或从模板创建工作流及初始修订     |
| `GET /api/v1/workflows/{workflowId}`                            | 读取元数据、活动修订和唯一运行实例摘要 |
| `PATCH /api/v1/workflows/{workflowId}`                          | 更新工作流名称和说明，不修改活动修订   |
| `GET/POST /api/v1/workflows/{workflowId}/revisions`             | 列表，或保存新不可变修订               |
| `GET /api/v1/workflows/{workflowId}/revisions/{revisionId}`     | 读取固定修订                           |
| `POST /api/v1/workflows/{workflowId}/lifecycle`                 | 执行 `start`、`pause` 或 `archive`     |
| `GET/POST /api/v1/workflows/{workflowId}/batches`               | 最近批次摘要，或创建手工批次           |
| `GET /api/v1/workflows/{workflowId}/activity`                   | 按单调游标读取持久活动                 |
| `GET /api/v1/workflows/{workflowId}/activity/ws`                | 游标补齐后推送活动增量                 |
| `GET /api/v1/batches/{batchId}`                                 | 读取批次、节点路径、活动和制品引用     |
| `POST /api/v1/batches/{batchId}`                                | 执行 `cancel`、`retry` 或 `replay`     |
| `GET /api/v1/artifacts/{sha256}/manifest`                       | 读取并校验制品清单                     |
| `GET /api/v1/artifacts/{sha256}/download`                       | 下载解压后的制品正文                   |
| `GET/POST /api/v1/result-views`                                 | 列出获授权视图，或由管理员创建固定视图 |
| `GET /api/v1/result-views/{viewId}`                             | 读取授权视图的公开描述                 |
| `PUT /api/v1/result-views/{viewId}/grants`                      | 管理员原子替换用户与角色授权           |
| `POST /api/v1/result-views/{viewId}/revoke`                     | 管理员不可逆撤销共享视图               |
| `GET /api/v1/result-views/{viewId}/batches`                     | 读取固定工作流的脱敏批次摘要           |
| `POST /api/v1/result-views/{viewId}/batches/{batchId}/{action}` | 按白名单重试或取消范围内批次           |
| `POST /api/v1/result-views/{viewId}/workflow/pause`             | 按白名单暂停固定工作流                 |

- 创建接受批次、事件、Connector 和 Quant 模板。事件 Trigger 按类型及可选精确 source/subject 过滤；定时配置只接受 UTC `everySeconds` 60 至 86400，不提供 Cron DSL。图 `schemaVersion` 固定为 `1`，节点保存 `nodeInstanceId`、精确节点版本、普通配置、结构化输入映射和位置；边保存两端端口及可选 Boolean CEL 条件。
- 输入映射只接受 `field`、`literal`、`cel`。字段来源使用上游 `nodeInstanceId` 和字段路径数组；保存校验端口、可达性、DAG、JSON Schema、字段类型和 CEL，并拒绝 Decimal CEL 算术。图级后向边始终拒绝；`core.loop` 只运行内嵌无环子图，并强制 1 至 100 次上限、绝对超时和 Boolean CEL 退出条件。每轮 NodeRun、Checkpoint 与操作键都包含迭代号，人工等待节点不能嵌入 Loop。
- 保存请求必须提供当前 `expectedActiveRevisionId`。服务锁定工作流，校验完整图，写入递增修订、修订级密钥绑定并原子切换活动指针；并发旧指针返回 `409 Conflict`，失败校验不创建修订。同一节点实例的类型和版本不变时保留持久状态；删除节点或修改类型/版本且已有状态时，工作流必须为 `paused`，并由管理员通过 `resetStateNodeInstanceIds` 精确确认要重置的节点，状态删除与修订激活在同一事务提交。
- `secretChanges` 只允许替换或移除节点 Config Schema 声明的顶层 `x-coinsphere-secret` 字段。密钥按修订、节点实例和字段独立加密；响应只返回 `secretFields[nodeInstanceId][field]=true`，图、修订响应和节点目录永不返回密钥值。
- 修订保存后不可更新或删除。归档工作流只读且不能重新启动；`needs_attention` 经人工处理后先回到 `paused`，`archive` 只允许从 `paused` 或 `needs_attention` 执行。
- `start` 允许批次队列领取工作并启动连续流 Trigger；`pause` 停止领取新批次、取消 Trigger，当前 Action 返回后保存检查点并重新排队。手工触发只适用于 `core.manual`，`core.schedule` 按 UTC 固定间隔去重入队。TriggerHandler 必须响应取消和 Emitter 背压；异常退出把工作流置为 `needs_attention`，进程重启会从数据库扫描仍在运行的连续流。
- CloudEvent 要求 1.0、UTC 时间、对象 `data` 和 1 至 256 字节 `partitionkey`。`(source,id)` 全局唯一；相同内容重试返回原事件，不同内容返回 `409`。事件、投递和批次在同一事务提交，Outbox 持久重试内部失败事件。
- 批次创建时固定事件与活动修订。单实例执行器使用 PostgreSQL 持久队列、每工作流并发/积压上限和有界 `stream`/`compute` 池；同工作流同分区按入队顺序领取，不同分区可并行，过期租约重启后重新排队。进入 `waiting` 的人工任务保存上下文并释放执行池和分区占用。
- 每个成功节点原子提交终态 NodeRun、输出 Checkpoint 和缓冲状态。失败只重试当前节点，默认最多三次并线性退避；操作键固定为 `sha256(batchId + ":" + nodeInstanceId + ":" + loopIteration)`。取消通过 `context.Context` 协作传递，取消请求后不再调度下游节点。最终失败用 Outbox 发布 `io.coinsphere.workflow.batch.failed`。
- 核心执行 `core.manual`、`core.schedule`、`core.event`、`core.constant`、`core.human_approval`、`core.loop` 和 `core.end`，其他 Action/Trigger 从编译期插件注册表调用；执行前后分别校验输入/输出 Schema，修订密钥通过节点范围 `SecretReader` 解密。启动前必须已配置活动修订的全部必需密钥。
- `core.human_approval` 默认产生 `pending` 任务；显式 `auto` 模式直接输出自动批准。相同工作流、节点和业务键的新任务会把旧任务置为 `superseded`。`approved`、`rejected`、`expired` 和 `superseded` 都只能提交一次并恢复原批次，决定正文最多 64 KiB。
- 终态批次可创建固定原事件与修订的诊断重放。`notification`、`human_action` 和 `paper` 副作用不再次执行，而是复用原 Checkpoint 和制品；缺少原 Checkpoint 时重放失败。
- 批次列表返回最近 100 条摘要；批次详情按执行顺序返回 NodeRun、受控活动摘要和制品引用。活动查询首次返回最近记录，后续使用 `after` 单调游标增量读取，单页上限 200。
- 活动由数据库在批次、NodeRun 和人工任务状态转换时原子追加，只包含事件类型、状态、受控中文摘要和错误类别，不保存原始错误、输入、输出或密钥。活动 WebSocket 要求同源 `Origin`，使用 `coinsphere.workflow-activity.v1` 与 Access Token 两个子协议值认证，并从 `after` 游标补齐；WebSocket 不是真实事实源。
- `ArtifactStore` 将最多 1 GiB 的正文用标准库 gzip 压缩并按未压缩正文 SHA-256 寻址。Checkpoint 原子引用清单；Manifest 在服务端重新计算大小和摘要，Web 下载后再次校验摘要。
- 终态批次、NodeRun、Checkpoint、批次活动和未被其他检查点引用的制品按工作流 `retentionDays` 清理，默认 30 天。制品数据库记录提交后再删除正文；失败最多留下无引用文件，不丢失仍被引用的正文。

## 插件清单

每个可信本地插件根目录包含唯一的 `coinsphere-plugin.json`：

- `schemaVersion` 当前固定为 `1`。
- `id` 是稳定的小写点分名称；`version` 是严格 SemVer。
- `sdkMajor` 必须等于当前 SDK major，`requiresCore` 必须包含当前 Core 版本。
- Backend 入口必须是拥有匹配 module 名的 Go module；Frontend 和 migration 路径必须留在插件根目录内。
- `contributes` 只接受 `nodes`、`triggers`、`apiRoutes`、`resultPages` 和 `migrations`，声明的非 migration 贡献必须实际注册。

`plugin validate` 只读校验一个或多个目录。应用启动只执行生成的 Go 注册表，不扫描插件目录或动态加载共享库。

## SDK

Action 描述符固定节点类型、SemVer、Config/UI/Input/Output Schema、执行池、副作用等级和状态模式。需要校验的 Schema 必须声明 JSON Schema 2020-12。

`ActionRequest` 包含固定工作流/修订、节点实例 ID、稳定操作键、已解析输入和配置，以及 SecretReader、StateStore、ArtifactStore 和结构化 Logger。`ActionResult` 只返回 JSON 输出和制品引用。P0 注册并可在契约测试上下文执行 Action；P1 工作流运行时已经接入全部上下文。

插件作用域路由必须声明一种上下文：

- `WorkflowScope`：当前工作流和节点范围，仅供工作流管理面。
- `ResultScope`：固定视图、插件、页面、服务端 scope/filter、操作白名单和当前用户；普通响应不返回固定 scope/filter 或源 workflow ID。
- `SystemScope`：插件安装状态和系统级健康范围。

插件不得从查询参数扩大核心注入的范围。不存在、未授权或已撤销 ResultView 统一返回 `404`；操作先解析 active 视图，再检查白名单、RBAC 和领域状态。

结果页描述符包含插件内唯一 `pageKey`、标题、前端组件入口、范围/过滤器 Schema、操作白名单和移动端能力。`FrontendPluginModule.pages` 可为插件提供独立菜单页，`resultPages` 提供限定工作流上下文的结果页；测试契约插件不加入生产菜单。

内置 `official.connector` 提供 HTTP Action、Webhook Trigger、WebSocket Trigger 和运行诊断结果页；`official.ai` 提供 OpenAI-compatible 结构化模型调用和结果页。两者只访问 `workflow.http_allowed_hosts` 的精确公共域名，禁用环境代理，拨号前后解析并拒绝非公网 IP。Binance 只允许明确列出的公共 GET/公共 WebSocket，授权、私有或未知端点一律拒绝。AI 节点只接收/返回 JSON 对象，不能控制工作流生命周期或交易。

内置 `official.quant` 提供 `binance_candles` Trigger、`evaluate`/`backtest`/`signal`/`paper_execute` Action、可信 SMA crossover 策略和移动可用结果页。行情只使用 Binance Spot/USD-M 公共接口，按 `market + instrument + interval` 合并订阅并发布 `market.candle.closed`；重复 REST/WebSocket 数据由 K 线键和 CloudEvent `(source,id)` 去重。策略只接收升序、连续、UTC 闭合 K 线和已校验参数，返回 `-1` 至 `1` 的 Decimal 目标；实时、回测与 Paper 调用同一 `Evaluate`。

Paper 账户按 `workflowId + paper nodeInstanceId` 唯一。默认人工审批；自动模式只有在最大总名义价值、单品种名义价值、单次操作名义价值、最大日亏损和最大回撤全部显式配置时有效。批准后重新读取 Binance 公共报价，并在一个数据库事务中检查信号/任务状态、报价新鲜度、品种状态、数量步进、账户状态及五项风险限制。拒绝不会创建账户或账本；成功执行写入不可变订单、成交、费用和账本事实，再更新账户与持仓投影。操作键和唯一约束保证节点重试、进程重启及 Outbox 重投不重复成交或投递。

内置 `official.notification` 提供 `in_app` Action 和投递结果页。站内投递只保存受控标题、正文和业务键，以操作键幂等；诊断重放复用原 Checkpoint，不再次产生 notification、human_action 或 paper 副作用。

`GET /api/v1/plugins/official.quant/{instruments|candles|strategies|backtests|signals|paper-accounts}`、Paper 账户重建和 Notification 系统查询是 `SystemScope` 路由，只允许超级管理员。ResultView 插件路由只接受核心注入的固定范围，查询参数不能扩大范围。金融值均返回十进制字符串。

匿名 Webhook 要求 `X-CoinSphere-Webhook-Secret`、`Idempotency-Key` 和 `X-CoinSphere-Partition-Key` 各出现一次，正文必须是不超过 1 MiB 的 JSON 对象。错误 Secret、非运行工作流和非 Webhook 主触发器统一返回不可发现响应。

## 生命周期与数据

- `plugin install` 校验源码、执行插件 migration、复制源码、生成 Go/Vue 注册表、更新 Go module 并构建 Backend/Web 镜像。
- `plugin upgrade` 只接受版本递增的同 major 升级；既有 migration 文件必须字节不变，新 migration 版本只能追加。
- 安装或升级失败恢复构建输入和操作前插件 migration 版本；构建命令不启动或切换运行容器。
- `plugin uninstall` 有活动引用时拒绝；成功后移除静态源码和注册并保留插件 schema。
- `plugin purge-data` 要求插件已卸载、无任何活动或历史引用，并精确提供 `PURGE <plugin-id>`；schema 和安装记录在同一事务删除。

核心及随应用发布的内置 Quant/Notification migration 使用 `schema_migrations`；领域数据仍由 `plugin_quant` 和 `plugin_notification` schema 拥有。通过 CLI 安装的插件使用独立 `plugin_<规范化 ID>` schema 和 `<schema>.schema_migrations`，卸载不执行 Down。P4 发布标签记录 migration freeze 提交；冻结后已有 migration 字节不变，只能追加更高版本。

## 尚未实现

Testnet/Live、私有交易 API、插件市场、签名、沙箱、热加载和多实例集群不属于 V2 当前合同。P4 合并、发布或部署不构成任何真实交易放行。
