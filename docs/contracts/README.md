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
| 通知 | `/api/v1/notification-deliveries`、`/api/v1/ws/notifications` |

交易账户、订单、仓位和 Executor API 尚未交付，不在当前公共契约中预留路由。

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

`manual` 信号在下一根 K 线闭合时过期，每个策略实例最多保留一条 `active` 手动信号；延迟完成的旧 K 线信号直接记为 `expired`。批准和拒绝只允许信号 Owner 对仍处于 `active` 且未过期的手动信号执行；重复决策、越权和过期决策均拒绝。两类命令都要求 `Idempotency-Key`，同键同请求返回原结果，同键不同决策返回冲突；批准还要求当前登录会话签发、五分钟有效的 `X-Reauth-Token`。批准只记录人工意图，不创建订单、仓位或 Executor 命令。

信号事件由 Go App 按固定规则幂等投递到站内通知，以及 Owner 已启用的钉钉机器人、QQ Bot 和 SMTP 渠道。每个信号和渠道最多一条投递记录：成功后重放跳过，失败由同一 Outbox 退避重试；某个通知渠道失败不得阻止已匹配工作流入队。站内通知列表返回信号模式、状态和过期时间，通知 WebSocket 只作实时提示，持久记录仍是离线与重连后的事实源。普通领域事件继续由工作流通知节点编排。

## 回测与产物

回测只消费闭合 K 线：当前 K 线闭合时计算目标，目标变化在下一根 K 线开盘按差额成交，最后一根信号不成交。Spot 不允许负仓位；USD-M 使用固定的窄范围 Decimal Bar 模型，费用、滑点、止损、资金费和同 Bar 冲突按保守规则计算，不模拟盘口排队、部分成交或价格改善。

手续费率、滑点率、止损参数和 USD-M `fundingRates` 都由回测请求显式提供并按 Decimal 校验。当前实现只保存并使用调用方提供的资金费率；它们不是 Binance 权威历史，不能单独作为晋级证据。USD-M 缺少资金费率时回测失败，Spot 不接受该字段。

结果和输入使用规范化十进制字符串、UTC 和固定模拟器版本记录 SHA-256。需要留存的回测由 Worker 写入本地内容寻址目录，包含输入、结果和 Manifest；只有成功完成哈希登记的产物才可被引用。

## Worker

任务队列使用 PostgreSQL。量化任务固定分为 `realtime` 和 `backtest` 两个 lane：各 lane 只认领自己的任务，backtest 使用受限子进程，不能占用 realtime 槽位。`strategy.realtime` 只消费已启用实例和闭合 K 线，信号、Outbox 与任务成功终态在同一租约保护事务提交。认领、心跳、取消、租约过期和崩溃恢复沿用数据库状态约束。

Worker 和策略子进程只接收规范化输入、策略文件、参数和独立产物目录；环境变量按白名单重建，不读取交易凭据，不调用交易所私有接口，不运行时安装依赖或启动逐任务 Docker。墙钟、CPU、内存和产物大小上限必须由部署配置提供。

## 未交付交易边界

当前代码不提供交易账户、下单或自动化能力。未来交易能力必须遵守 [ADR-0010](../architecture/decisions/0010-execution-risk-events.md)：Executor 是唯一私有交易接口边界，风控上限缺失时保持禁用，未知订单先对账，急停后只允许减仓/平仓/撤单，并经用户手工晋级。
