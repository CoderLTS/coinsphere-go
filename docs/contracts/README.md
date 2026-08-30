# CoinSphere 公共契约

本文只冻结当前已实现的跨模块语义。系统结构见[当前架构](../architecture/overview.md)，插件包与示例见[插件开发指南](../plugin-development.md)；未注册路由不得视为可用接口。

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
- 旧行情、策略、回测、信号、全局通知渠道/规则和交易路由已移除，不提供别名或兼容响应。

## 工作流

| 路由                                                            | 语义                                   |
| --------------------------------------------------------------- | -------------------------------------- |
| `GET /api/v1/workflows/templates`                               | 列出当前可创建的批处理、事件和连续流模板 |
| `POST /api/v1/events`                                           | 发布 CloudEvents 1.0 结构化 JSON       |
| `POST /api/v1/webhooks/{workflowId}`                            | 通过工作流 Secret 发布 Webhook 事件    |
| `GET /api/v1/human-tasks`                                       | 查询待处理人工任务                     |
| `POST /api/v1/human-tasks/{taskId}`                             | 一次性批准或拒绝人工任务               |
| `GET /api/v1/workflows/node-definitions`                        | 列出核心与编译期插件节点 Schema        |
| `POST /api/v1/workflows/validate`                               | 只读校验完整工作流图                   |
| `GET/POST /api/v1/workflows`                                    | 列表，或从模板创建工作流及初始修订     |
| `GET /api/v1/workflows/{workflowId}`                            | 读取元数据、活动修订和运行时容量       |
| `PATCH /api/v1/workflows/{workflowId}`                          | 更新工作流名称和说明，不修改活动修订   |
| `GET/POST /api/v1/workflows/{workflowId}/revisions`             | 列表，或保存新不可变修订               |
| `GET /api/v1/workflows/{workflowId}/revisions/{revisionId}`     | 读取固定修订                           |
| `POST /api/v1/workflows/{workflowId}/lifecycle`                 | 执行 `activate` 或 `deactivate`        |
| `GET/POST /api/v1/workflows/{workflowId}/runs`                  | 搜索运行日志，或创建手工运行           |
| `WS /api/v1/ws/workflows/{workflowId}/runs`                    | 超级管理员订阅轻量运行更新通知         |
| `GET /api/v1/workflow-runs/{runId}`                             | 读取事件、节点尝试、日志和制品引用     |
| `POST /api/v1/workflow-runs/{runId}`                            | 执行 `cancel`、`retry` 或 `replay`     |
| `GET /api/v1/notification-deliveries`                          | 查询当前用户的站内通知与未读数         |
| `POST /api/v1/notification-deliveries/{deliveryId}/read`       | 将当前用户的一条站内通知标为已读       |
| `POST /api/v1/notification-deliveries/read-all`                | 将当前用户的全部站内通知标为已读       |
| `WS /api/v1/ws/notifications`                                  | 订阅当前用户的站内通知和未读数更新     |
| `GET /api/v1/artifacts/{sha256}/manifest`                       | 读取并校验制品清单                     |
| `GET /api/v1/artifacts/{sha256}/download`                       | 下载解压后的制品正文                   |
| `GET/POST /api/v1/result-views`                                 | 列出获授权视图，或由管理员创建固定视图 |
| `GET /api/v1/result-views/{viewId}`                             | 读取授权视图的公开描述                 |
| `PUT /api/v1/result-views/{viewId}/grants`                      | 管理员原子替换用户与角色授权           |
| `POST /api/v1/result-views/{viewId}/revoke`                     | 管理员不可逆撤销共享视图               |
| `GET /api/v1/result-views/{viewId}/runs`                        | 读取固定工作流的脱敏运行摘要           |
| `POST /api/v1/result-views/{viewId}/runs/{runId}/{action}`      | 按白名单重试或取消范围内运行           |
| `POST /api/v1/result-views/{viewId}/workflow/pause`             | 按白名单暂停固定工作流                 |

- 创建接受批处理、事件、Connector 和 Quant 模板。事件 Trigger 按类型及可选精确 source/subject 过滤；`core.schedule` 配置必须二选一：`everySeconds` 60 至 86400，或六段 `cronExpression` 加 IANA `timeZone`。图 `schemaVersion` 固定为 `1`，节点保存 `nodeInstanceId`、精确节点版本、普通配置、结构化输入映射和位置；边保存两端端口及可选 Boolean CEL 条件。
- 输入映射只接受 `field`、`literal`、`cel`。字段来源使用上游 `nodeInstanceId` 和字段路径数组；保存校验端口、可达性、DAG、JSON Schema、字段类型和 CEL，并拒绝 Decimal CEL 算术。图级后向边始终拒绝；`core.loop` 只运行内嵌无环子图，并强制 1 至 100 次上限、绝对超时和 Boolean CEL 退出条件。每轮 RunNode、RunCheckpoint 与操作键都包含迭代号，人工等待节点不能嵌入 Loop。
- 保存请求必须提供当前 `expectedActiveRevisionId`。服务锁定工作流，校验完整图，写入递增修订、修订级密钥绑定并原子切换活动指针；并发旧指针返回 `409 Conflict`，失败校验不创建修订。同一节点实例的类型和版本不变时保留持久状态；删除节点或修改类型/版本且已有状态时，工作流必须为 `inactive`，并由管理员通过 `resetStateNodeInstanceIds` 精确确认要重置的节点，状态删除与修订激活在同一事务提交。
- `secretChanges` 只允许替换或移除节点 Config Schema 声明的顶层 `x-coinsphere-secret` 字段。密钥按修订、节点实例和字段独立加密；响应只返回 `secretFields[nodeInstanceId][field]=true`，图、修订响应和节点目录永不返回密钥值。
- 修订保存后不可更新或删除。工作流状态只有 `inactive / active / error`；Trigger 异常退出进入 `error`，管理员先 `deactivate` 恢复为 `inactive`，确认修复后再 `activate`。
- `activate` 允许 Run 队列领取工作并启动连续流 Trigger；`deactivate` 停止领取新 Run、取消 Trigger，当前 Action 返回后保存检查点并重新排队。手工触发只适用于 `core.manual`；`core.schedule` 按固定间隔或带时区 Cron 去重入队，服务恢复后最多补一次漏跑。TriggerHandler 必须响应取消和 Emitter 背压；进程重启会从数据库扫描仍为 `active` 的连续流。
- CloudEvent 要求 1.0、UTC 时间、对象 `data` 和 1 至 256 字节 `partitionkey`。`(source,id)` 全局唯一；相同内容重试返回原事件，不同内容返回 `409`。事件、投递和 Run 在同一事务提交，Outbox 持久重试内部失败事件。闭合 K 线的 OHLCV 正文只保存在 Quant 行情表和事件表，Run 只关联事件 ID。
- Run 创建时固定事件与活动修订。单实例执行器使用 PostgreSQL 持久队列、每工作流并发/积压上限和有界 `stream`/`compute` 池；同工作流同分区按入队顺序领取，不同分区可并行，过期租约重启后重新排队。进入 `waiting` 的人工任务保存上下文并释放执行池和分区占用。
- 每个成功节点原子提交终态 RunNode、输出 RunCheckpoint 和缓冲状态。失败只重试当前节点，默认最多三次并线性退避；操作键固定为 `sha256(runId + ":" + nodeInstanceId + ":" + loopIteration)`。取消通过 `context.Context` 协作传递，取消请求后不再调度下游节点。最终失败用 Outbox 发布 `io.coinsphere.workflow.run.failed`。
- 核心执行 `core.manual`、`core.schedule`、`core.event`、`core.constant`、`core.human_approval`、`core.loop` 和 `core.end`，其他 Action/Trigger 从编译期插件注册表调用；执行前后分别校验输入/输出 Schema，修订密钥通过节点范围 `SecretReader` 解密。启动前必须已配置活动修订的全部必需密钥。
- `core.human_approval` 默认产生 `pending` 任务；显式 `auto` 模式直接输出自动批准。相同工作流、节点和业务键的新任务会把旧任务置为 `superseded`。`approved`、`rejected`、`expired` 和 `superseded` 都只能提交一次并恢复原 Run，决定正文最多 64 KiB。
- 终态 Run 可创建固定原事件与修订的诊断重放。`notification`、`human_action` 和 `paper` 副作用不再次执行，而是复用原 RunCheckpoint 和制品；缺少原检查点时重放失败。
- Run 列表支持游标分页、UTC `from/to`、状态、触发类型和最多 200 字符的关键词搜索。详情按执行顺序返回全部 RunNode 尝试、节点多行日志、脱敏输入输出摘要、事件摘要、结果和制品引用。前端按每个 attempt 合成开始、业务和结束记录，开始展示输入摘要，结束展示状态、耗时、输出摘要或受控错误；历史页选择的 Run 固定展示，不跟随后续流式 Run。
- 核心和插件通过节点范围 `slog.Logger` 写入 `workflow_node_logs`。消息最多 1000 字符，结构化字段最多 4 KiB，只保留受限标量；密钥、令牌、授权头、Cookie、DSN 和原始载荷统一丢弃或脱敏。不提供第二套 Activity API。运行详情 WebSocket 使用 `coinsphere.workflow-runs.v1` 子协议、同源 Origin 和超级管理员 Access Token，只发送轻量更新通知；客户端仍从 HTTP API 读取持久事实。
- `ArtifactStore` 将最多 1 GiB 的正文用标准库 gzip 压缩并按未压缩正文 SHA-256 寻址。Checkpoint 原子引用清单；Manifest 在服务端重新计算大小和摘要，Web 下载后再次校验摘要。
- 终态 Run、RunNode、节点日志、RunCheckpoint 和未被其他检查点引用的制品按工作流 `retentionDays` 清理，默认 30 天。制品数据库记录提交后再删除正文；失败最多留下无引用文件，不丢失仍被引用的正文。

## 插件清单

每个可信本地插件根目录包含唯一的 `coinsphere-plugin.json`：

- `schemaVersion` 当前固定为 `1`。
- `id` 是稳定的小写点分名称；`version` 是严格 SemVer。
- `sdkMajor` 必须等于当前 SDK major，`requiresCore` 必须包含当前 Core 版本。
- Backend 入口必须是拥有匹配 module 名的 Go module；Frontend 和 migration 路径必须留在插件根目录内。
- `contributes` 只接受 `nodes`、`triggers`、`strategies`、`apiRoutes`、`pages`、`resultPages` 和 `migrations`，声明的非 migration 贡献必须实际注册。

`plugin validate` 只读校验一个或多个目录。应用启动只执行生成的 Go 注册表，不扫描插件目录或动态加载共享库。

## SDK

Action 描述符固定节点类型、SemVer、Config/UI/Input/Output Schema、可选固定 `Branches`、执行池、副作用等级和状态模式。需要校验的 Schema 必须声明 JSON Schema 2020-12。分支节点输出 `branch` 后，运行时先选择同名端口，再执行该边原有 Boolean CEL；端口允许零条或多条出边，图仍必须是 DAG，目标节点类型不受限制。

`ActionRequest` 包含固定工作流/修订、节点实例 ID、稳定操作键、已解析输入和配置，以及 SecretReader、StateStore、ArtifactStore 和结构化 Logger。`ActionResult` 只返回 JSON 输出和制品引用。契约测试和工作流运行时使用同一 Registry 与 Handler 接口。

插件作用域路由必须声明一种上下文：

- `WorkflowScope`：SDK 已定义的工作流和节点范围；当前公共 HTTP 未挂载该作用域，插件不得假定存在可调用 URL。
- `ResultScope`：固定视图、插件、页面、服务端 scope/filter、操作白名单和当前用户；普通响应不返回固定 scope/filter 或源 workflow ID。
- `SystemScope`：插件安装状态和系统级健康范围。

插件不得从查询参数扩大核心注入的范围。不存在、未授权或已撤销 ResultView 统一返回 `404`；操作先解析 active 视图，再检查白名单、RBAC 和领域状态。

页面描述符通过 `pages` 注册插件内唯一的 `pageKey`、标题、图标和缓存设置，并生成独立顶级菜单；前端 `FrontendPluginModule.pages` 必须导出同名页面。`resultPages` 只渲染服务端固定 ResultView 范围，不生成菜单。

内置 `official.connector` 提供 HTTP Action、Webhook Trigger、WebSocket Trigger 和运行诊断结果页；`official.ai` 提供 OpenAI-compatible 结构化模型调用和结果页。两者只访问 `workflow.http_allowed_hosts` 的精确公共域名，禁用环境代理，拨号前后解析并拒绝非公网 IP。Binance 只允许明确列出的公共 GET/公共 WebSocket，授权、私有或未知端点一律拒绝。AI 节点只接收/返回 JSON 对象，不能控制工作流生命周期或交易。

内置 `official.quant@1.2.0` 提供 `binance_candles` Trigger、`sync_instruments`/`evaluate`/六种指标判断/`backtest`/`signal`/`paper_execute` Action、可信 SMA crossover 策略和移动可用结果页。`sync_instruments` 在所有选中 Spot/USD-M 公共元数据均成功解析后，用事务级 advisory lock 原子替换当前工作流的过滤快照；白名单取交集、黑名单任一命中即排除，全局目录取全部工作流来源并集。应用首次缺少该节点时创建并激活北京时间每六小时运行的默认工作流并立即首跑，后续启动不重新激活已停用工作流。K 线连接只负责补数和实时采集，按 `market + instrument + interval` 合并订阅并发布 `market.candle.closed`；重复 REST/WebSocket 数据由 K 线键和 CloudEvent `(source,id)` 去重。策略只接收升序、连续、UTC 闭合 K 线和已校验参数，返回 `-1` 至 `1` 的 Decimal 目标；实时、回测与 Paper 调用同一 `Evaluate`。

六种 `1.0.0` 判断节点分别是 `official.quant.volume_spike_condition`、`official.quant.price_change_condition`、`official.quant.macd_condition`、`official.quant.kdj_condition`、`official.quant.rsi_condition` 和 `official.quant.bollinger_condition`。一个节点只保存一种指标规则及市场、交易对、检查周期、K 线周期和名称；每次在当前与上一个检查时点截取当时已闭合的 K 线，禁止未来数据。K 线断档、非法参数和数据库错误使节点失败，历史不足则 `ready=false` 并走 `false`。EMA、Wilder RSI、KDJ、布林标准差和有界平方根全部使用确定性 Decimal。

判断输出包含 `ready`、`matched`、`previousMatched`、`branch`、`entered`、`triggered`、当前/上一值、UTC 时间、业务键和中文摘要。`true` 串联表达 AND，并行汇合表达 OR，`false` 可连接任意节点。`branch` 始终反映当前结果，连续命中仍执行下游；`entered` 沿判断连线传播路径重新进入状态，`triggered` 只在整条 true 路径重新进入时成立。编辑器连接判断节点到任一通知节点时自动附加 `input.triggered == true` 并聚合摘要，因此连续命中只通知一次，恢复后再次命中会重新通知。

Paper 账户按 `workflowId + paper nodeInstanceId` 唯一。默认人工审批；自动模式只有在最大总名义价值、单品种名义价值、单次操作名义价值、最大日亏损和最大回撤全部显式配置时有效。批准后重新读取 Binance 公共报价，并在一个数据库事务中检查信号/任务状态、报价新鲜度、品种状态、数量步进、账户状态及五项风险限制。拒绝不会创建账户或账本；成功执行写入不可变订单、成交、费用和账本事实，再更新账户与持仓投影。操作键和唯一约束保证节点重试、进程重启及 Outbox 重投不重复成交或投递。

内置 `official.notification@1.1.0` 提供 `in_app`、`dingtalk`、`qq` 和 `smtp` 四个独立 Action 及统一投递页。四种节点统一接收 `subjectKey/message` 并按稳定操作键幂等；站内节点把用户和当前启用角色成员合并去重，旧配置没有目标时投递给工作流创建者。外部节点失败保存受控错误类别并交给当前节点最多三次重试，已成功操作直接复用。

钉钉和 QQ 只访问固定官方域名，使用 8 秒超时、禁止重定向和 64 KiB 响应上限；QQ Token 按凭据指纹缓存。SMTP 只接受解析到公网地址的域名和 TLS/STARTTLS，STARTTLS 不可用时失败而不降级。Access Token、Client Secret、SMTP 密码及钉钉签名 Secret 均为修订级 `x-coinsphere-secret`，不进入图、日志或投递表。

站内通知按 `recipient_user_id` 隔离，查询和已读操作只影响当前用户。持久投递提交后才发布 `notice.created`；实时 WebSocket 使用 `coinsphere.notifications.v1` 子协议携带 Access Token，要求同源 Origin、有界发送队列和 Ping/Pong，断线或队列满不影响数据库事实。诊断重放复用原 Checkpoint，不再次产生 notification、human_action 或 paper 副作用。

`GET /api/v1/plugins/official.quant/{instruments|candles|strategies|backtests|signals|paper-accounts}`、Paper 账户重建和 Notification 系统查询是 `SystemScope` 路由，只允许超级管理员。ResultView 插件路由只接受核心注入的固定范围，查询参数不能扩大范围。金融值均返回十进制字符串。

匿名 Webhook 要求 `X-CoinSphere-Webhook-Secret`、`Idempotency-Key` 和 `X-CoinSphere-Partition-Key` 各出现一次，正文必须是不超过 1 MiB 的 JSON 对象。错误 Secret、非运行工作流和非 Webhook 主触发器统一返回不可发现响应。

## 生命周期与数据

- `plugin install` 校验源码、执行插件 migration、复制源码、生成 Go/Vue 注册表、更新 Go module 并构建 Backend/Web 镜像。
- `plugin upgrade` 只接受版本递增的同 major 升级；既有 migration 文件必须字节不变，新 migration 版本只能追加。
- 安装或升级失败恢复构建输入和操作前插件 migration 版本；构建命令不启动或切换运行容器。
- `plugin uninstall` 有活动引用时拒绝；成功后移除静态源码和注册并保留插件 schema。
- `plugin purge-data` 要求插件已卸载、无任何活动或历史引用，并精确提供 `PURGE <plugin-id>`；schema 和安装记录在同一事务删除。

核心及随应用发布的内置 Quant/Notification migration 使用 `schema_migrations`；领域数据仍由 `plugin_quant` 和 `plugin_notification` schema 拥有。通过 CLI 安装的插件使用独立 `plugin_<规范化 ID>` schema 和 `<schema>.schema_migrations`，卸载不执行 Down。正式 Paper 观察开始前必须记录 migration freeze 提交；冻结后已有 migration 字节不变，只能追加更高版本。

## 尚未实现

Testnet、Live、私有交易 API、插件市场、签名、沙箱、热加载和多实例集群不属于当前合同。代码合并、发布、部署或 Paper 观察不构成任何真实交易放行。
