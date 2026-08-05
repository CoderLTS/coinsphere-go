# CoinSphere 公共契约

本文冻结自用优先架构下的公共类型、信任边界和跨模块语义。具体实现进度以 GitHub 为准；任何实现 Issue 若需要改变本契约，必须先修订 ADR 与本文件。

## 通用类型

- 金融时间统一使用 UTC。数据库使用 `timestamptz`，HTTP 和事件使用带 `Z` 的 RFC3339Nano；领域逻辑不得依赖本地时区。
- 价格、数量、金额、费率、盈亏和目标仓位使用 Decimal；数据库使用 `numeric(38,18)`，JSON 使用十进制字符串，账务值禁止使用 `float64`。
- 新金融资源 ID 使用 UUIDv7 字符串。旧后台资源可以在原子 `/api/v1` 迁移时保留既有 ID，但不得把旧 ID 约定扩散到新金融资源。
- 新金融 HTTP DTO 使用强类型结构。`map[string]any` 只允许留在动态工作流图、外部无类型载荷和旧后台内部边界。
- 所有外部枚举和金融状态在信任边界严格校验；未知值不能静默降级为默认值。

核心枚举固定为：

```text
Market           = spot | usd_m
TradingEnv       = paper | testnet | live
ExecutionMode    = signal_only | manual | auto
WorkerLane       = realtime | backtest
DecisionStatus   = not_required | pending | approved | rejected | expired
ExecutionStatus  = not_requested | queued | blocked | submitted | completed | failed
```

## 身份与资源归属

- 不提供公开注册 API 或注册页面。用户只能由管理员创建、禁用或重置密码。
- 登录页、登录接口、健康检查和静态资源是仅有的匿名入口；其他页面、HTTP API 和 WebSocket 都要求登录。
- Binance 品种、K 线和管理员发布的策略版本向所有登录用户共享只读。
- 自选、策略实例、回测、信号、交易账户、风险、订单、仓位、通知渠道和投递记录必须携带 `ownerUserId`，普通资源查询始终按当前用户过滤。
- 管理员用户管理、策略发布和自动化审批使用独立管理接口。普通资源接口不因调用者是管理员而自动取消所有者过滤。
- 首期使用应用层所有者过滤、复合外键和唯一约束，不引入 RLS。跨用户关联必须在数据库约束或同一事务的信任边界校验中拒绝。

## HTTP 与 WebSocket

OpenAPI 3.0.3 是 `/api/v1` 的唯一来源。迁移实施时必须在一个 PR 中同步修改 OpenAPI、生成 Go/TypeScript 类型、后端路由、前端调用、WebSocket 和测试，随后删除旧 `/api`；不保留重定向、别名或兼容层。

目标路由分组固定为：

| 领域   | 路由                                                                                                  |
| ------ | ----------------------------------------------------------------------------------------------------- |
| 身份   | `/api/v1/auth/login`、`logout`、`reauth`、`/api/v1/me`、`/api/v1/admin/users`                         |
| 行情   | `/api/v1/markets/symbols`、`/markets/candles`、`/watchlists`                                          |
| 策略   | `/api/v1/strategies`、`/admin/strategies`、`/strategy-instances`、`/backtests`                        |
| 信号   | `/api/v1/signals`、`/signals/{id}/approve`、`/signals/{id}/reject`                                    |
| 交易   | `/api/v1/trading-accounts`、`/risk-limits`、`/positions`、`/orders`、`/automation`、`/emergency-stop` |
| 通知   | `/api/v1/notification-channels`、`/notification-deliveries`、`/ws/notifications`                      |
| 工作流 | 现有工作流资源统一迁移到 `/api/v1/workflows` 下                                                       |

- 错误响应使用 `application/problem+json`，至少包含 `type`、`title`、`status`、`detail` 和 `requestId`。
- 列表默认 50、最大 200，使用不透明游标并以唯一 ID 作为最后稳定排序键。筛选或排序改变后旧游标无效。
- 创建异步任务、批准信号、自动化开关、急停和其他命令型写操作要求 `Idempotency-Key`。普通键保留 24 小时；交易键、请求摘要和结果与订单审计同寿命。同键不同载荷返回 `409`。
- `POST /api/v1/auth/login` 是唯一匿名身份 API；登录成功返回 Access Token，Token 失效后重新登录，不提供公开刷新入口。
- `POST /api/v1/auth/reauth` 接收当前密码并返回绑定当前用户和会话的短期不透明 `reauthToken`，服务端只保存其哈希，五分钟后失效。敏感命令通过 `X-Reauth-Token` 提交；过期、跨用户或跨会话 Token 均拒绝。

WebSocket 路径为 `GET /api/v1/ws/notifications`。握手继续使用固定 `Sec-WebSocket-Protocol: coinsphere.notifications.v1, <access-token>`，禁止查询串 Token。事件信封固定为：

```json
{
  "type": "signal.created",
  "version": 1,
  "sequence": 42,
  "occurredAt": "2026-08-05T08:00:00.000000000Z",
  "data": {}
}
```

每条连接只有一个 writer；`sequence` 从 1 连续递增，重连后重置。持久通知记录是事实源，进程内唤醒和 WebSocket 只负责在线提示；离线用户通过未读快照恢复。

## Binance 行情

V1 只接受：

```go
type Venue string
const VenueBinance Venue = "binance"

type Market string
const (
    MarketSpot Market = "spot"
    MarketUSDM Market = "usd_m"
)
```

`Instrument` 至少包含 `id`、`venue`、`market`、`nativeSymbol`、`baseAsset`、`quoteAsset`、`status`、`priceTick`、`quantityStep`、`minQuantity`、`minNotional` 和 UTC 更新时间。Go App 同步 Binance Spot 与 USD-M 全量元数据，但只订阅自选和启用策略引用的数据流。

`Candle` 固定包含：

```text
instrumentId, interval, openTime, closeTime,
open, high, low, close, baseVolume, isClosed
```

- `openTime` 按 Unix epoch 对齐；`closeTime` 是排他的下一根边界。Binance 原始闭区间 `closeTime` 加 1 ms 后必须等于该边界。
- OHLCV 使用 Decimal，并满足 `low <= min(open, close)`、`high >= max(open, close)` 和非负成交量。
- 最低周期为 `1m`；只保存策略、自选或回测实际使用的 Binance 原生周期，不自行从 1m 生成第二事实源。
- 实时闭合状态只信任 Binance Kline 的 `k.x`。未闭合记录可以幂等更新；闭合记录默认冻结，只有显式修复任务可以覆盖并记录原因和质量事件。
- 唯一键为 `instrumentId + interval + openTime`。WebSocket、REST 历史和断线补数进入同一规范化与 Upsert 路径。
- 历史数据按回测窗口请求补齐，单次分页游标只保证窗口内确定前进。REST 同周期结果是修复权威来源。
- `market_candles` 使用 7 天 chunk、30 天后压缩和默认两年 retention。策略候选引用的精确数据通过冻结产物保护，不延长全部在线 K 线寿命。
- 只有闭合 K 线成功提交后才能创建实时策略任务；未闭合更新、Ticker 和补数中的旧 K 线不得重复触发同一信号。

最新 Ticker 只用于展示、风险行情新鲜度和批准时估值，不保存 Ticker 历史。执行价格和订单状态以 Executor 从交易所获得的权威响应为准。

## 策略版本与实例

只有管理员可以创建和修改策略草稿。V1 策略只有一个 Python 文件，发布时固定以下元数据：

```text
strategyVersionId, codeSha256, runtimeVersion,
market, symbol, interval, lookbackBars, parameterSchema
```

`parameterSchema` 只支持命名的 `integer | decimal | boolean | string` 标量、必填、默认值以及适用的最小/最大值或枚举；不支持嵌套对象、动态代码、依赖声明或运行时安装。

策略入口固定为：

```python
def on_bar(
    candles: Sequence[Candle],
    params: Mapping[str, JSONScalar],
) -> Decimal:
    ...
```

- `candles` 只包含按时间升序排列的闭合 K 线，长度不超过 `lookbackBars`；时间为 UTC，OHLCV 为 `Decimal`。
- 函数必须无外部副作用，不访问网络、数据库、当前时间、随机源或交易凭据。
- 返回值是相对于策略实例 `allocationUsdt` 的目标仓位。Spot 合法范围为 `0..1`，USD-M 为 `-1..1`，`0` 表示平仓。
- 回测和实时使用完全相同的代码、参数、输入类型和范围校验。异常、超时、非 Decimal、NaN/Infinity 或越界结果使任务失败；实时任务还必须暂停对应实例并产生关键通知，不能生成交易意图。
- 已发布版本不可修改或删除；更新代码或元数据必须发布新版本。普通用户只能读取已发布版本。

策略实例属于用户，至少固定 `strategyVersionId`、验证后的参数、`allocationUsdt`、执行模式、可选交易环境、可选账户和启用状态。已用于信号或回测的实例配置采用版本快照，修改配置不能改写历史结果。

`signal_only` 不要求交易账户；`manual` 和 `auto` 必须绑定同一所有者的账户及适用环境。`auto` 还要求管理员对该实例版本授权，以及所有者显式启用。

## 回测与冻结产物

回测只消费闭合 K 线，并逐根调用 `on_bar`：

1. 在当前 K 线闭合后计算目标仓位。
2. 目标变化在下一根 K 线开盘按差额成交，最后一根信号不产生没有下一开盘价的成交。
3. Spot 禁止负仓位；USD-M 固定逐仓、单向和显式低杠杆。
4. 手续费率和滑点基点是回测请求的显式 Decimal 参数，不提供隐藏默认值。
5. USD-M 使用冻结窗口内的 Binance 资金费历史，并在对应 UTC 时间计入；缺失资金费使回测失败。
6. 止损、跳空、强平或同 Bar 冲突无法确定先后时使用对策略更不利的可行路径；不模拟盘口排队、部分成交或价格改善。

结果至少包含目标序列、规范化订单、成交、手续费、资金费、权益曲线、最大回撤、收益和强平事件。所有金额和费率使用 Decimal 字符串。

普通回测记录策略版本、参数、数据范围、规范化输入 SHA-256、模拟器版本和结果 SHA-256。申请 Paper/Testnet/Live 晋级时必须冻结：

- 按唯一键排序、字段顺序固定、Decimal 使用规范字符串、UTC 使用 RFC3339Nano 的 `JSONL.gz` 数据。
- 策略源文件、参数、运行时版本、模拟器版本和完整结果。
- 列出每个文件 SHA-256、大小和引用关系的 Manifest。

产物先写临时路径，完成压缩和哈希校验后原子移动到内容寻址目录并登记。失败临时文件可按 Runbook 清理；被晋级证据引用的产物不得自动删除。

## Worker 队列与隔离

现有异步任务状态和租约协议保持不变，量化任务增加固定 `lane`：

- `realtime` 消费者只认领实时策略任务，并始终保留一个执行槽。
- `backtest` 消费者只认领回测任务，并始终限制为一个子进程槽。

队列在各 lane 内按显式优先级、创建时间和唯一 ID 稳定认领。Backtest 不能借用 realtime 槽，realtime 也不在 backtest 子进程内执行。

Worker 的墙钟超时、CPU、内存和产物空间上限必须在部署配置中显式提供；缺失或非法配置拒绝启动。生产 Linux 使用子进程资源限制和有界终止，开发环境使用相同墙钟与取消契约。

任务子进程只获得规范化输入、策略文件、参数和独立产物目录；环境变量经过白名单重建。Worker 和子进程都不读取交易凭据或调用交易所私有接口。不再启动逐任务 Docker，也不支持自定义镜像、自定义依赖或运行时安装。

实时任务从 Go App 接收闭合事件到信号成功持久化的正常负载 p99 目标为两秒。每次超时和排队超限都记录固定分类指标，但不得记录策略源代码、参数载荷或行情原始载荷。

## 信号、批准与仓位归属

每个信号固定策略实例、策略版本、触发 K 线、当前目标仓位、前一目标仓位、创建时间和适用账户。唯一约束 `strategyInstanceId + strategyVersionId + candleCloseTime` 防止重复闭合事件生成第二个信号。

信号使用独立的决策和执行状态：

- `signal_only`：`decisionStatus=not_required`、`executionStatus=not_requested`。
- `manual`：创建时为 `pending`，`expiresAt` 等于下一根 K 线闭合时间；可以进入 `approved | rejected | expired`。批准后执行状态从 `queued` 开始。
- `auto`：管理员授权和用户开关有效时 `decisionStatus=not_required` 并尝试排队；任一权限或风控缺失时 `executionStatus=blocked`。

批准和拒绝必须由信号所有者执行，并要求未过期信号、五分钟内的 `X-Reauth-Token` 和 `Idempotency-Key`。批准按当时最新有效报价和账户状态重新风控，不使用信号生成时价格保证成交。

通知操作 URL 使用至少 128 位随机 Token，数据库只保存哈希，并绑定用户、信号、用途和 `min(signal.expiresAt, tokenMaxExpiry)`。GET 只导航到站内页面，不能改变状态；成功使用、拒绝、过期或信号终态后 Token 失效。

同一 `account + market + symbol` 最多一个 manual/auto 策略实例拥有活动仓位。数据库使用活动所有权记录和部分唯一约束保证；`signal_only` 不占用所有权。只有仓位归零且在途订单和保护单完成对账后才能释放所有权。

## 交易账户、凭据与风险

交易账户属于用户并固定 `paper | testnet | live` 环境和 `spot | usd_m` 市场。Paper 账户不含凭据；Testnet/Live 凭据经 HTTPS 提交，使用现有对称主密钥加密，API 永不返回密文或明文。

保存凭据前，用户必须显式确认：

- 交易所密钥已关闭提现权限。
- 已配置固定出口 IP 白名单。

V1 不自动调用 Binance 检查权限。测试、Issue、PR、CI、日志和 AI 上下文不得出现真实密钥；更新凭据必须提交完整新值，不能读取旧值。

账户启用自动化前必须完整配置：

```text
allowedSymbols
maxAccountGrossNotionalUsdt
maxSymbolNotionalUsdt
maxOrderNotionalUsdt
maxDailyLossUsdt
maxDrawdownRatio
maxQuoteAgeSeconds
```

USD-M 账户还必须显式配置固定低杠杆，并强制 `isolated + one_way`。策略实例必须配置 `allocationUsdt` 和保护止损比例；策略级金额限制只能等于或严于账户限制。

任一适用限制缺失、非法或无法计算时，自动化保持禁用。风险触发后暂停账户及相关实例，禁止增仓，只允许减仓、平仓和撤单。全局急停优先于账户、策略和批准状态；解除急停需要五分钟复验和独立审计记录，不能自动恢复已暂停策略。

## Executor 与保护单

Go App 只写入交易意图；Executor 认领后再次执行权限、风控、行情新鲜度和仓位所有权校验。意图状态固定为 `queued | processing | submitted | reconciling | completed | blocked | failed`。

- 每个意图拥有稳定 `intentId` 和由账户、意图、用途派生的确定性 `clientOrderId`。
- 网络超时或响应不完整时进入 `reconciling`，先按 `clientOrderId` 查询；只有交易所明确不存在原订单时才允许继续创建。
- 自动调仓只使用市价差额单。请求数量必须按 `quantityStep` 向不增加风险的方向取整，并重新检查最小数量、最小名义金额和账户上限。
- Spot 成交后按当前受管仓位数量创建或替换交易所侧止损；在新保护单确认前，旧保护单不得提前失效到留下无保护窗口。
- USD-M 使用一向持仓的 `STOP_MARKET`，固定 `closePosition=true`、`workingType=MARK_PRICE`，不同时提交 `quantity` 或 `reduceOnly`。
- 任何保护单无法确认时，Executor 立即使用市价减仓至零，暂停实例并生成关键通知；若平仓状态未知，进入对账并保持账户禁止增仓。

工作流、通知 Bot、Go App 的通用 HTTP 节点和 Python Worker 均不能写入已通过 Executor 边界的订单状态，也不能直接调用交易所私有接口。

## 交易事件与投影

本地权威事实只包含追加式领域事件：

```text
order_events, fill_events, fee_events, funding_events
```

事件必须包含环境、账户、市场、品种、外部唯一标识、发生时间、接收时间和 Decimal 金融值。交易所重复回报由外部唯一标识和事件类型约束幂等；修正通过新的更正事件表达，不更新历史事件正文。

`orders`、`positions`、`balances` 和 `pnl` 是可重建投影。每次在线更新和全量重建必须使用同一归约逻辑；重建结果与在线投影逐字段一致才算通过。V1 不建设复式分录、通用总账或通用事件溯源框架。

## 内嵌通知网关

通知扩展现有 `notification_channels`、`notification_deliveries`、加密配置、站内未读和工作流通知节点。内部最小发送契约为：

```go
type Sender interface {
    Send(context.Context, ChannelConfig, Message) (Receipt, error)
}
```

Provider 静态固定为 `in_app | dingtalk_webhook | qq_bot | smtp_email`。不提供动态注册、运行时插件或独立通知服务；企业微信只在产生实际需求后新增同类适配器。

`Message` 固定标题、正文、级别、可选站内操作 URL 和去重键。投递以 `domainEventId + channelId` 唯一，失败按现有 Outbox/投递重试记录处理；日志只记录投递 ID、渠道类型和固定错误分类，不记录密钥、完整 URL、原始响应或正文。

- 钉钉沿用现有签名和 Webhook 实现，增加 ActionCard URL 行为；每个机器人遵守 20 条/分钟限制，普通消息可合并，关键消息只延迟重试、不丢弃。
- QQ 使用官方 Bot HTTP 鉴权和群/C2C Markdown 消息，域名白名单、ICP备案和固定出口 IP 是部署前置；不引入完整 Bot WebSocket/Redis SDK。
- SMTP 和站内通知沿用现有实现。渠道测试只发送明确标记的测试消息并保存脱敏结果。

固定关键事件为：待人工批准信号、订单未知或失败、保护单失败、风控暂停和全局急停。它们始终持久化站内通知，并尝试用户配置的外部渠道；投递失败不得绕过或反向改变交易风控状态。普通量化事件继续由工作流决定是否通知。

## 工作流边界

- 量化、风险和交易模块通过领域 Outbox 发布版本化事件；工作流只能以资源 ID 和脱敏摘要作为输入。
- 工作流通知节点调用内嵌通知网关；通用 HTTP 节点继续受精确域名白名单和 SSRF 防护约束。
- 工作流不能获得交易凭据、Executor 私有网络、逐 K 线回调或下单接口。任何试图把交易命令路由到工作流的变更都必须被契约测试拒绝。
- 关键交易通知不依赖用户工作流是否存在、启用或成功，避免工作流配置错误掩盖安全事件。
