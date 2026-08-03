# 公共契约

## 金融公共类型

- 时间统一使用 UTC。数据库使用 `timestamptz`，HTTP 和事件使用 RFC3339Nano；领域逻辑不得依赖本地时区。
- 价格、数量、金额和费率在 Go 领域中使用 `github.com/shopspring/decimal`，数据库使用 `numeric(38,18)`，JSON 使用十进制字符串；金融计算和账务禁止使用 `float64`。
- 新金融 HTTP 接口使用强类型请求/响应 DTO。`map[string]any` 只允许留在旧管理接口、动态工作流图和外部无类型载荷边界，不得作为 `/api/v1` 金融资源模型。
- 金融资源 ID 使用 UUIDv7 字符串。

## HTTP API

- 新金融接口统一位于 `/api/v1`，现有管理接口保持原路径直至单独迁移。
- 列表接口使用游标分页，命令接口支持 `Idempotency-Key`。
- 错误响应使用 `application/problem+json`，至少包含 `type`、`title`、`status`、`code`、`requestId`、`retryable`。
- 当前 A1 登录、refresh 和 logout 接口通过 JSON 传递 Access/Refresh Token，前端状态同时持有两者。A1 安全波次的目标契约是 Access Token 只保存在浏览器内存中，Refresh Token 改为由 refresh/logout 接口轮换的 HttpOnly Cookie；目标实现合并前不得把它描述为现行行为。

## A2 行情契约范围（待实现）

本节只限定 A2 首个契约 PR 的边界，尚不是现行运行时契约；以下约束必须与 Binance/OKX 可执行样本一同合并后才生效。首个交付只冻结 Decimal/UTC 规则、`Instrument`、`Candle`、`Ticker`、最小数据库约束和 `MarketSource`。缺口质量模型、推荐算法、保留策略、衍生统计和 UI 查询模型留到对应纵向切片，不进入公共契约。

### 规范化类型

| 类型 | 最小语义 |
| --- | --- |
| `Instrument` | `id`、`venue`、`marketType`、`nativeSymbol`、`baseAsset`、`quoteAsset`、`status`、价格步长、数量步长 |
| `Candle` | `venue`、`instrumentId`、`interval`、`openTime`、`closeTime`、OHLC、基础成交量 |
| `Ticker` | `venue`、`instrumentId`、`occurredAt`、最新价、最优买价、最优卖价 |

所有时间必须是 UTC，所有价格、步长和成交量必须是 Decimal。交易所原始枚举和字段不得泄漏到这些公共类型；无法无损规范化的字段留在交易所适配器内，直到 Binance 与 OKX 都出现真实共同需求。

### 最小存储约束

- Instrument 以 `(venue, marketType, nativeSymbol)` 唯一，规范化 ID 创建后保持稳定。
- K 线以 `(venue, instrumentId, interval, openTime)` 唯一；OHLC 和成交量列使用 `numeric(38,18)`，时间列使用 `timestamptz`。
- Ticker 首轮只承诺可按 `(venue, instrumentId)` 读取最新快照及其事件时间，不提前冻结历史保留模型。
- 批量写入必须幂等；重复补数不得生成第二条逻辑记录。

### `MarketSource`

`MarketSource` 只覆盖 Binance 与 OKX 都需要的三个外部能力：品种元数据快照、分页历史 K 线和实时 Candle/Ticker 订阅。它接收 Context、返回上述规范化类型，并将交易所协议错误分类后交给共享 runner；连接生命周期、限频、断点、重试、日志和持久化由共享 runner 负责。

该接口不是动态插件点。A2 契约必须同时带有 Binance 和 OKX 脱敏样本的可执行检查，证明：

- 两组原始载荷产生相同语义的规范化类型。
- Decimal 解析和 JSON 往返不经过浮点数，时间统一为 UTC。
- K 线唯一键稳定，重复样本写入保持幂等。
- 分页边界和实时订阅取消不会重复或遗漏样本声明范围内的数据。

契约合并后，Binance、OKX、共享存储/runner 和市场 UI 才从同一 base 并行实现，不得在各切片中扩展公共字段。

## 实时事件

WebSocket 事件统一使用以下信封：

```json
{
  "type": "market.candle.updated",
  "version": 1,
  "sequence": 42,
  "occurredAt": "2026-07-31T08:00:00.000000000Z",
  "data": {}
}
```

信封 `version` 固定为 `1`，且只包含 `type`、`version`、`sequence`、`occurredAt`、`data` 五个字段。`sequence` 按单条连接实际写出的业务帧从 `1` 连续递增，重连后重置；RFC6455 Ping/Pong 等控制帧不占序号。`occurredAt` 是事件进入实时通道时的 UTC RFC3339Nano 时间，同一事件广播到多个连接时保持一致。客户端遇到未知类型可忽略业务内容，但应消费其合法序号；版本不支持或序号重复、倒退时不得更新状态。

`GET /ws/notifications` 发送两类通知事件：

- `notice.unread`：`data` 为 `{"unreadCount": 0}`。
- `notice.created`：`data` 为 `{"record": {}, "unreadCount": 1}`。

每条通知连接只有一个 writer，业务帧和 Ping 均由它写入。发送队列有界；队列满时服务端关闭慢连接，不阻塞生产者、不静默丢弃后续帧，客户端重连后以首个 `notice.unread` 快照恢复。服务端周期发送 RFC6455 Ping，Pong 延长读期限，失联连接到期关闭；Hub 关机后拒绝新连接并等待既有 writer 退出。

浏览器握手必须携带唯一且合法的 `Origin`，其有效 scheme、主机和端口必须与请求完全同源；缺失、畸形、跨 scheme/主机/端口均拒绝。当前 A1 通知 WebSocket 通过 URL 查询参数传递 Access Token，代理、应用和测试日志不得记录查询串、令牌或事件 payload。A1 安全波次将改为固定 `Sec-WebSocket-Protocol` 鉴权并拒绝查询串 Token；开发和生产反向代理始终必须保留原始 Host（含非默认端口）及合法的有效 scheme。

## 异步任务

任务状态固定为 `queued`、`claimed`、`running`、`cancelRequested`、`succeeded`、`failed`、`canceled`。正常路径为 `queued -> claimed -> running -> succeeded/failed`；取消可从活跃状态进入 `cancelRequested -> canceled`。每次认领递增 `attempt_count` 并生成新的唯一 `lease_id`，启动、心跳和终态写入必须同时匹配任务 ID、租约 ID、合法前态及未过期的数据库时间。

Worker 必须周期续租；租约一旦过期，旧 Worker 立即失去心跳和终态写权限。过期的 `claimed/running` 在 `attempt_count < max_attempts` 时清除旧租约并重新排队，否则进入 `failed`。心跳观察到 `cancelRequested` 后不得继续续租；该状态在租约过期或取消请求满 4 秒时直接进入 `canceled`，禁止重试。任务取消从请求提交到伪任务停止并进入 `canceled` 不得超过 5 秒，即使 Owner 在观察取消后、确认终态前崩溃也必须满足该时限。

## Outbox 投递租约

Outbox 认领必须在单条数据库语句中完成候选选择、批量状态更新和结果返回，禁止先查后改。每条 `pending` 事件仅在 `available_at` 已到且 `attempt_count < max_attempts` 时可进入 `claimed`；认领递增一次尝试次数，并由数据库生成新的唯一 `lease_id`、记录 Owner、认领时间和租约到期时间。

续租、`claimed -> processed` 和订阅失败释放都必须同时匹配事件 ID、`lease_id`、Owner、`attempt_count` 和未过期的数据库时间。租约过期后旧 Owner 立即失去续租与终态写权限；即使事件已经恢复并重新认领，旧 token 的写入也只能影响零行。订阅失败保留已消耗的尝试次数并清空旧租约：仍有次数时按数据库时间与 `retry_backoff_seconds` 回到 `pending`，最后一次失败或租约过期时进入 `dead_letter`，且 `processed_at` 与 `dead_lettered_at` 相同。当前 `failed` 只表示旧 dispatcher 的既有终态，新 dispatcher 不再写入该状态。

工作流成功、最终失败和 stale 耗尽时，execution 终态、attempt、两条标准事件及对应入口状态必须在同一短事务提交；任一事件插入失败时整体回滚。显式 `event.emit` 节点的权威业务动作就是单条 Outbox 插入，不把整张图或外部副作用包入长事务。未告警死信通过原子设置 `alerted_at` 由一个实例领取，告警日志只允许固定 Outbox ID、尝试次数和分类，不得包含 payload、metadata、Owner、token 或异常正文。该标记提供 at-most-once 日志去重，不等同于可靠外部告警。

## 交易命令

所有订单意图必须携带稳定 `intentId` 和确定性 `clientOrderId`。订单状态未知时先对账，禁止通过无条件重试创建第二笔订单。
