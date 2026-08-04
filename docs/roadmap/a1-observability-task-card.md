# A1 可观测性波次任务卡

| 字段 | 内容 |
| --- | --- |
| Base commit | `ffefad15aa5f9437c9eb521fe5ed646230fa6fc7` |
| 目标 | 使用 Go 标准库提供 JSON 结构化日志、HTTP Request ID、持久化审计、有限低基数指标、进程存活检查和 PostgreSQL 就绪检查，并保持 A1 既有安全、并发和事务契约。 |
| 输入契约 | 现有 `config.yml`、PostgreSQL 连接、`net/http` 请求以及已认证 `Principal`；合法上游 Request ID 仅允许 1-64 个 ASCII 字母、数字、`.`、`_`、`-`。 |
| 输出契约 | JSON 日志写标准输出；每个 HTTP 响应返回 `X-Request-ID`；固定无业务标签的进程指标；存活检查只反映进程，就绪检查只反映 PostgreSQL Ping；匹配到的写请求持久化动作、主体、结果和 Request ID，但不保存请求/响应正文、查询串、Header、令牌、凭据、错误正文或个人资料。 |
| 允许修改路径 | `backend/main.go`、`backend/internal/api/**`、`backend/internal/db/**`、`backend/internal/service/**`、`backend/internal/migration/sql/00002_a1_observability.sql`、相关 Go 测试、`docker-compose.yml`、`deploy/production/**`、`docs/architecture/**`、`docs/contracts/**`、`docs/runbooks/**`、本任务卡。 |
| 禁止修改路径 | 已应用的 `backend/internal/migration/sql/00001_a1_postgres_baseline.sql`、`backend/go.mod`、`backend/go.sum`、`frontend/**`、`worker/**`、真实凭据、生产数据、交易能力和自动合并配置。 |
| 验收命令 | 仓库根目录执行一次 `powershell -ExecutionPolicy Bypass -File .\scripts\verify.ps1`；本机缺失 Docker/WSL，Compose、镜像和 PostgreSQL/TimescaleDB 完整层由 Draft PR 的 GitHub Actions 验证。 |
| 回滚方式 | `git revert` 本波次提交；代码回滚默认保留 `audit_records`。仅在确认审计表为空且所有写入者停止后，才执行 `coinsphere-migrate -direction down -steps 1` 删除本次表。 |

## 事务与失败边界

- 业务动作继续使用现有领域事务；HTTP 审计在处理完成后以独立短事务插入，审计失败不得伪装成业务回滚，也不得诱发客户端重试已提交动作。
- 审计写入失败只记录固定分类和 Request ID，并增加固定指标；日志和指标不包含数据库错误正文或请求载荷。
- 本波次不建设 tracing、外部指标系统、日志文件轮转、动态采集插件或 A2 领域能力。
