# 公共契约

## HTTP API

- 新金融接口统一位于 `/api/v1`，现有管理接口保持原路径直至单独迁移。
- 新金融资源使用 UUIDv7 字符串 ID，时间使用 UTC RFC3339Nano。
- 价格、数量、金额和费率使用 Decimal，JSON 中序列化为字符串。
- 列表接口使用游标分页，命令接口支持 `Idempotency-Key`。
- 错误响应使用 `application/problem+json`，至少包含 `type`、`title`、`status`、`code`、`requestId`、`retryable`。

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

当前信封 `version` 固定为 `1`，且只包含 `type`、`version`、`sequence`、`occurredAt`、`data` 五个字段。`sequence` 按单条连接实际写出的业务帧从 `1` 连续递增，重连后重置；RFC6455 Ping/Pong 等控制帧不占序号。`occurredAt` 是事件进入实时通道时的 UTC RFC3339Nano 时间，同一事件广播到多个连接时保持一致。客户端遇到未知类型可忽略业务内容，但应消费其合法序号；版本不支持或序号重复、倒退时不得更新状态。

`GET /ws/notifications` 当前发送两类通知事件：

- `notice.unread`：`data` 为 `{"unreadCount": 0}`。
- `notice.created`：`data` 为 `{"record": {}, "unreadCount": 1}`。

每条通知连接只有一个 writer，业务帧和 Ping 均由它写入。发送队列有界；队列满时服务端关闭慢连接，不阻塞生产者、不静默丢弃后续帧，客户端重连后以首个 `notice.unread` 快照恢复。服务端周期发送 RFC6455 Ping，Pong 延长读期限，失联连接到期关闭；Hub 关机后拒绝新连接并等待既有 writer 退出。

浏览器握手必须携带唯一且合法的 `Origin`，其有效 scheme、主机和端口必须与请求完全同源；缺失、畸形、跨 scheme/主机/端口均拒绝。开发和生产反向代理必须保留原始 Host（含非默认端口）及合法的有效 scheme。当前访问令牌仍通过 `token` 查询参数传递，存储、Cookie 化和轮换由 A1-7 独立交付；代理和应用日志不得记录该查询值或事件 payload。

## 异步任务

任务状态固定为 `queued`、`claimed`、`running`、`cancelRequested`、`succeeded`、`failed`、`canceled`。正常路径为 `queued -> claimed -> running -> succeeded/failed`；取消可从活跃状态进入 `cancelRequested -> canceled`。每次认领递增 `attempt_count` 并生成新的唯一 `lease_id`，启动、心跳和终态写入必须同时匹配任务 ID、租约 ID、合法前态及未过期的数据库时间。

Worker 必须周期续租；租约一旦过期，旧 Worker 立即失去心跳和终态写权限。过期的 `claimed/running` 在 `attempt_count < max_attempts` 时清除旧租约并重新排队，否则进入 `failed`。心跳观察到 `cancelRequested` 后不得继续续租；该状态在租约过期或取消请求满 4 秒时直接进入 `canceled`，禁止重试。任务取消从请求提交到伪任务停止并进入 `canceled` 不得超过 5 秒，即使 Owner 在观察取消后、确认终态前崩溃也必须满足该时限。

## Outbox 投递租约

Outbox 认领必须在单条数据库语句中完成候选选择、批量状态更新和结果返回，禁止先查后改。每条 `pending` 事件仅在 `available_at` 已到且 `attempt_count < max_attempts` 时可进入 `claimed`；认领递增一次尝试次数，并由数据库生成新的唯一 `lease_id`、记录 Owner、认领时间和租约到期时间。

续租、`claimed -> processed` 和订阅失败释放都必须同时匹配事件 ID、`lease_id`、Owner、`attempt_count` 和未过期的数据库时间。租约过期后旧 Owner 立即失去续租与终态写权限；即使事件已经恢复并重新认领，旧 token 的写入也只能影响零行。订阅失败保留已消耗的尝试次数并清空旧租约：仍有次数时按数据库时间与 `retry_backoff_seconds` 回到 `pending`，最后一次失败或租约过期时进入 `dead_letter`，且 `processed_at` 与 `dead_lettered_at` 相同。当前 `failed` 只表示旧 dispatcher 的既有终态，新 dispatcher 不再写入该状态。

工作流成功、最终失败和 stale 耗尽时，execution 终态、attempt、两条标准事件及对应入口状态必须在同一短事务提交；任一事件插入失败时整体回滚。显式 `event.emit` 节点的权威业务动作就是单条 Outbox 插入，不把整张图或外部副作用包入长事务。未告警死信通过原子设置 `alerted_at` 由一个实例领取，告警日志只允许固定 Outbox ID、尝试次数和分类，不得包含 payload、metadata、Owner、token 或异常正文。该标记提供 at-most-once 日志去重，不等同于可靠外部告警。

## 交易命令

所有订单意图必须携带稳定 `intentId` 和确定性 `clientOrderId`。订单状态未知时先对账，禁止通过无条件重试创建第二笔订单。
