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

## 异步任务

任务状态固定为 `queued`、`claimed`、`running`、`cancelRequested`、`succeeded`、`failed`、`canceled`。Worker 必须持有可过期租约并发送心跳；租约失效后任务才允许被其他 Worker 回收。

## 交易命令

所有订单意图必须携带稳定 `intentId` 和确定性 `clientOrderId`。订单状态未知时先对账，禁止通过无条件重试创建第二笔订单。
