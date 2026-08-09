# CoinSphere 公共契约

本文只冻结当前已实现的跨模块语义。HTTP 路由以 Go 注册表和边界测试为准，前端调用与测试必须在同一 PR 中同步变更，不维护重复的生成契约。动态进度和晋级证据只保存在 GitHub。

## 通用边界

- 金融时间统一使用 UTC。数据库使用 `timestamptz`，HTTP、事件和产物使用带 `Z` 的 RFC3339；领域逻辑不得依赖本地时区。
- 价格、数量、金额、费率和盈亏使用 Decimal；数据库金融列使用 `numeric(38,18)`，JSON 使用十进制字符串，账务值禁止使用 `float64`。
- 新行情、策略和回测资源使用 UUIDv7 字符串；既有后台资源保留现有 ID 语义，不为兼容旧系统新增别名。
- 外部枚举、时间、ID、Decimal 和文件路径在信任边界严格校验；未知值、非有限数和错误时区不能静默降级。

## 身份与归属

- 不提供公开注册 API 或页面。匿名入口只有登录、健康检查和静态资源；其他页面、HTTP API 和 WebSocket 均要求登录。
- Binance 品种、K 线和已发布策略版本是登录用户共享的只读资源；自选、策略实例、信号、回测和通知投递按当前用户隔离。
- 管理员通过独立权限管理接口维护用户和策略草稿。管理员身份不会让普通资源查询自动跨越所有者过滤。
- 首期使用应用层所有者过滤、数据库外键和唯一约束，不引入 RLS。跨用户关联必须被拒绝。

## HTTP 与 WebSocket

- 所有新金融接口使用 `/api/v1`；不保留旧 `/api` 或旧 WebSocket 路径的重定向、别名和兼容层。
- 错误响应使用 `application/problem+json`，包含 `type`、`title`、`status`、`detail` 和 `requestId`。
- 列表使用不透明游标和稳定 ID 排序；默认和上限由服务端统一校验，过滤或排序改变后旧游标无效。
- 会改变状态或创建异步任务的命令使用 `Idempotency-Key`；同键不同载荷返回冲突。
- `POST /api/v1/auth/login` 是唯一匿名身份 API。`POST /api/v1/auth/reauth` 返回绑定当前用户和会话、五分钟失效的不透明 Token；服务端只保存哈希，敏感命令通过 `X-Reauth-Token` 提交。

当前金融相关路由只有：

| 领域 | 路由 |
| --- | --- |
| 身份 | `/api/v1/auth/*`、`/api/v1/me`、`/api/v1/admin/users` |
| 行情 | `/api/v1/markets/symbols`、`/api/v1/markets/candles`、`/api/v1/watchlists` |
| 策略 | `/api/v1/admin/strategies`、`/api/v1/strategies`、`/api/v1/strategy-instances` |
| 回测 | `/api/v1/backtests` |
| 信号 | `/api/v1/signals`、`/api/v1/signals/{signalId}/approve`、`/api/v1/signals/{signalId}/reject` |
| 交易 | `/api/v1/trading/*`、`/api/v1/admin/trading/*` |
| 通知 | `/api/v1/notification-deliveries`、`/api/v1/ws/notifications` |

交易路由管理 Paper/Testnet 账户、风险限额、自动化开关/授权、全局急停及其只读投影；Live 账户仍被数据库禁止。账户创建、风险修改、恢复、自动化切换、授权和急停命令都要求 `Idempotency-Key`；风险修改、账户恢复、启用自动化、管理员授权和解除急停还要求有效的 `X-Reauth-Token`。

Testnet 凭据通过 `PUT /api/v1/trading/accounts/{accountId}/credentials` 保存，通过 `POST /api/v1/trading/accounts/{accountId}/credentials/revoke` 撤销。两条命令同时要求 `Idempotency-Key` 和 `X-Reauth-Token`；保存还要求调用方明确确认已关闭提现并配置 IP 白名单。凭据使用应用主密钥加密，API 只返回配置与验证状态，永不返回 API Key、Secret 或密文。保存后的状态固定为 `unverified`，账户保持暂停且自动实例被关闭；后续 Testnet Executor 验证凭据并完成首次对账前，账户不能恢复或启用执行。

WebSocket 使用 `GET /api/v1/ws/notifications`，通过固定子协议携带 Access Token，禁止查询串 Token。事件信封固定包含 `type`、`version`、`sequence`、`occurredAt` 和 `data`；每条连接只有一个 writer，持久通知记录才是事实源，重连后通过未读快照恢复。

## Binance 行情

V1 只接受 Binance 的 `spot` 和 `usd_m` 市场，以及交易所原生 `1m`、`5m`、`15m`、`1h`、`4h`、`1d` 周期。品种至少包含交易所、市场、原生代码、基础/计价资产、状态、价格最小变动、数量步长、最小数量、最小名义金额和 UTC 更新时间。

K 线至少包含 `instrumentId`、`interval`、`openTime`、`closeTime`、`open`、`high`、`low`、`close`、`baseVolume` 和 `isClosed`。时间必须按周期对齐，OHLC 范围合法且成交量非负；重复键为 `instrumentId + interval + openTime`。

- 只有 Binance Kline 的 `k.x=true` 才能把 K 线视为闭合；未闭合记录可幂等更新，闭合后普通写入不得覆盖。
- WebSocket、REST 历史和断线补数进入同一规范化 Upsert 路径。Ticker 只用于展示和新鲜度，不形成历史事实源。
- 历史回填按请求窗口使用游标分页；同一输入重复回填结果必须一致。

## 策略

只有管理员可以创建、修改和发布可信的单文件 Python 策略。发布版本固定源代码 SHA-256、运行时版本、Binance 市场/品种/周期、回看窗口和参数 Schema；已发布版本不可修改或删除。

入口为：

```python
def on_bar(candles: Sequence[Candle], params: Mapping[str, JSONScalar]) -> Decimal:
    ...
```

`candles` 只能是按 UTC 升序排列的闭合 K 线，OHLCV 使用 Decimal，长度不超过回看窗口。策略不得访问网络、数据库、当前时间、随机源或交易凭据；返回目标仓位必须为有限 Decimal，Spot 在 `0..1`，USD-M 在 `-1..1`。

实时和回测共用同一入口、参数校验和运行时；异常、超时、非有限值或越界结果使任务失败，不能生成交易意图。

## 实时信号与人工决策

用户从已发布策略版本创建 `signal_only | manual | auto`、`paper | testnet | live` 两个维度的策略实例；新实例默认关闭。启用实例订阅对应 Binance 闭合 K 线，每个实例和 K 线只生成一条持久信号。实时 Worker 复用策略 `on_bar` 契约，并把信号与 `strategy.signal.created` Outbox 事件放在同一事务提交。

`manual` 信号在下一根 K 线闭合时过期，每个策略实例最多保留一条 `active` 手动信号；延迟完成的旧 K 线信号直接记为 `expired`。批准和拒绝只允许信号 Owner 对仍处于 `active` 且未过期的手动信号执行；重复决策、越权和过期决策均拒绝。两类命令都要求 `Idempotency-Key`，同键同请求返回原结果，同键不同决策返回冲突；批准还要求当前登录会话签发、五分钟有效的 `X-Reauth-Token`。批准 Paper/Testnet 手动信号时在同一事务创建唯一交易意图；自动信号由 Outbox 消费路径幂等创建同环境意图。两条路径都不直接创建订单或调用交易所，Live 执行意图仍不可用。

信号事件由 Go App 按固定规则幂等投递到站内通知，以及 Owner 已启用的钉钉机器人、QQ Bot 和 SMTP 渠道。每个信号和渠道最多一条投递记录：成功后重放跳过，失败由同一 Outbox 退避重试；某个通知渠道失败不得阻止已匹配工作流入队。站内通知列表返回信号模式、状态和过期时间，通知 WebSocket 只作实时提示，持久记录仍是离线与重连后的事实源。普通领域事件继续由工作流通知节点编排。

## 回测与产物

回测只消费闭合 K 线：当前 K 线闭合时计算目标，目标变化在下一根 K 线开盘按差额成交，最后一根信号不成交。Spot 不允许负仓位；USD-M 使用固定的窄范围 Decimal Bar 模型，费用、滑点、止损、资金费和同 Bar 冲突按保守规则计算，不模拟盘口排队、部分成交或价格改善。

手续费率、滑点率、止损参数和 USD-M `fundingRates` 都由回测请求显式提供并按 Decimal 校验。当前实现只保存并使用调用方提供的资金费率；它们不是 Binance 权威历史，不能单独作为晋级证据。USD-M 缺少资金费率时回测失败，Spot 不接受该字段。

结果和输入使用规范化十进制字符串、UTC 和固定模拟器版本记录 SHA-256。需要留存的回测由 Worker 写入本地内容寻址目录，包含输入、结果和 Manifest；只有成功完成哈希登记的产物才可被引用。

## Worker

任务队列使用 PostgreSQL。量化任务固定分为 `realtime` 和 `backtest` 两个 lane：各 lane 只认领自己的任务，backtest 使用受限子进程，不能占用 realtime 槽位。`strategy.realtime` 只消费已启用实例和闭合 K 线，信号、Outbox 与任务成功终态在同一租约保护事务提交。认领、心跳、取消、租约过期和崩溃恢复沿用数据库状态约束。

Worker 和策略子进程只接收规范化输入、策略文件、参数和独立产物目录；环境变量按白名单重建，不读取交易凭据，不调用交易所私有接口，不运行时安装依赖或启动逐任务 Docker。墙钟、CPU、内存和产物大小上限必须由部署配置提供。

## 交易账户与 Executor

Paper 账户默认暂停，全局急停默认开启。账户只有在品种白名单、总名义金额、单品种、单笔、日亏损、最大回撤、行情最大年龄以及适用杠杆全部配置后才能恢复；自动模式还必须同时具备管理员授权、账户开关和已启用策略实例。缺少任一条件时保持禁用并暂停，不做静默降级。

Go Paper Executor 按账户串行消费持久意图，每次执行前重新检查所有者绑定、策略/信号状态、白名单、急停、账户状态、授权、风险上限、行情新鲜度和仓位归属。急停或风控状态下只允许减少同方向既有仓位；解除急停不会自动恢复账户、自动化或策略实例。

Paper 只追加 `order/fill/fee/funding` 事件，订单、仓位、余额和盈亏均可从事件重建。Paper Executor 启动只恢复 Paper 意图、只重建 Paper 账户投影，绝不领取 Testnet 意图；部署回滚不得执行 migration Down、删除交易事件或清空投影。

Testnet 私有访问默认关闭。显式启用后，只有 Go Executor 会解密凭据，并向 Spot `/api/v3/account`、`/api/v3/openOrders`、`/api/v3/order` 与 USD-M `/fapi/v3/account`、`/fapi/v1/openOrders`、`/fapi/v1/order` 发送带 UTC `timestamp`、`recvWindow` 和 HMAC-SHA256 签名的请求。API Key 只进入 `X-MBX-APIKEY` 请求头；认证、权限、限流、时钟偏差、协议和网络失败只保存固定脱敏错误码。

凭据验证成功后，Executor 把余额、USD-M 仓位和开放订单写入绑定当前凭据版本的独立 Testnet 投影。不可交易权限、开放订单、非白名单资产、Spot 既有持仓、USD-M 既有仓位或双向持仓模式都会形成固定 `mismatch`；外部协议或网络失败形成 `unknown`。只有 `matched` 允许用户手工恢复账户，后台对账不会自动恢复账户、启用自动化、修改外部订单或创建交易意图；凭据或风险白名单变化会清空旧投影并重新暂停账户。

首次对账 `matched`、账户手工恢复且适用风控与授权均通过后，Executor 才会按账户串行领取 Testnet 意图。每个主市价单使用意图生成的确定性 `clientOrderId`；提交前持久化 `prepared`，进程恢复或响应未知时先用同一 ID 查询，只有交易所明确返回不存在才允许重试。HTTP 拒单也先进入 `unknown` 并查询，避免把重复客户端订单号误判为未成交；外部请求不占用数据库事务，返回结果仅在账户与凭据版本仍有效时写入。USD-M 减仓携带 `reduceOnly=true`，任一协议、状态或风险差异都会保留未知态或暂停账户。

保护单、未知外部订单归属恢复以及成交、费用和资金费的持续权威对账仍未交付。生产继续保持 Testnet 私有能力关闭；后续晋级遵守 [ADR-0010](../architecture/decisions/0010-execution-risk-events.md) 并由用户手工放行。
