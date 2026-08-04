# ADR-0003：采用标准库结构化日志与进程内可观测性基线

- 状态：已接受
- 日期：2026-08-04

## 背景

A1 已具备 PostgreSQL、并发、认证和安全边界，但应用日志仍是无结构文本，HTTP 请求没有稳定关联 ID，写操作缺少持久化审计，单一 `/health` 也无法区分进程存活与数据库就绪。当前部署规模不需要 tracing 平台、外部指标 SDK 或动态采集插件。

## 决策

- 应用使用 Go `log/slog` 的 JSON Handler 输出到标准输出，`log.level` 控制最低级别；GORM 继续保留参数化 SQL 与固定错误分类，并通过同一 JSON Handler 输出。
- 每个 HTTP 请求使用 `X-Request-ID`。上游值仅在匹配 `[A-Za-z0-9._-]{1,64}` 时复用，否则由 `crypto/rand.Text` 生成；该值进入请求 Context、响应 Header、请求日志和审计记录。
- 所有匹配路由的 `POST`、`PUT`、`PATCH`、`DELETE` 请求在处理完成后写入 `audit_records`。只保存 Request ID、历史内部用户 ID、路由动作、无查询串资源路径、结果、HTTP 状态和 UTC 时间；用户删除不会改写历史 ID。不保存 Header、查询串、请求/响应正文、令牌、凭据、错误正文、IP 或个人资料。
- 业务动作保持现有领域事务。审计使用独立短事务，因此审计失败不能宣称业务已回滚，也不能把已提交动作改写成可重试响应；失败只产生固定 `database` 分类日志和 `coinsphere_audit_write_failures_total`。
- `/health/live` 只表示进程能响应；`/health/ready` 使用一秒上限执行 PostgreSQL `PingContext`；兼容 `/health` 是就绪别名。失败响应只返回固定状态，不包含 DSN、主机、schema 或驱动错误。
- `/metrics` 使用 Prometheus 文本格式，只暴露请求总数、失败数、在途数、审计写失败数和进程运行秒数五个固定无标签指标。

## 结果

- 日志、请求和审计可通过 Request ID 关联，指标基数不随用户、资源或路由增长。
- 审计记录存在时，`00002_a1_observability.sql` 的 Down 会失败并原子保留数据。
- 当前不保证业务行与审计行原子提交。只有监管或交易命令要求强原子审计时，才为对应领域命令设计同事务审计，不把该复杂度扩散到全部旧管理接口。
- tracing、日志采集、长期指标存储和告警路由留给出现跨进程诊断或保留期需求后的独立决策。
