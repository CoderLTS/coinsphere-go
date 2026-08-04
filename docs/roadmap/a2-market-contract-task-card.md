# A2 行情最小契约实现任务卡

## 执行身份与基线

| 项目 | 冻结值 |
| --- | --- |
| 执行模型 | `gpt-5.6-terra`，思考等级 `max` |
| PR base commit | `10dc4188a4e24881fce1676ebcdf06d6a3a48fd0` |
| 工作分支 | `codex/a2-market-contract` |
| 交付形态 | A2 首个可独立验收和回滚的纵向 PR；设计文档、Go 契约、migration 与可执行样本同一 PR |
| 权威规格 | [公共契约：A2 行情最小契约](../contracts/README.md#a2-行情最小契约) |

该 base 是 PR 相对 `main` 的共同基线。Terra 必须从本任务的 Sol 设计检查点继续，不得丢弃设计提交、改写 base 或把用户未跟踪的 `.codegraph/` 纳入提交。

## 目标

一次交付以下可执行契约：

- `decimal.Decimal`、UTC、UUIDv7 的共享边界与校验。
- `InstrumentMetadata`、`Instrument`、`Candle`、`Ticker`、冻结枚举与 `MarketSource`。
- `market_instruments`、`market_candles`、`market_ticker_snapshots` 三张普通 PostgreSQL 表及受保护 Down。
- Binance/OKX 公开协议的纯 JSON 规范化函数、脱敏 fixture 和表驱动契约测试。
- migration 生命周期、约束、唯一键、标准冲突写入和非空 Down 的 PostgreSQL 测试。

本任务不实现真实 HTTP/WebSocket、collector、共享 runner、具体 PostgreSQL Store、行情 API、前端、补数调度、质量报告、Timescale hypertable、保留策略或后续 A2/A3 能力。

## 冻结输入与输出

### 依赖与公共类型

- 只新增两个直接依赖：`github.com/shopspring/decimal v1.4.0` 与 `github.com/google/uuid v1.6.0`；后者已在 `go.sum` 中作为传递依赖存在。
- 精确字段、JSON 名、枚举值和不变量以公共契约为准，不得添加 `raw`、`metadata`、quote volume、精度位数、更新时间或单所枚举字段。
- 外部元数据输入规范化为不带 ID 的 `InstrumentMetadata`。测试在首次标准 upsert 时提供 UUIDv7；自然键冲突必须保留原 ID，再构造完整 `Instrument` 供 Candle/Ticker 规范化使用。
- 原始 Decimal 字段必须先进入 Go `string`；禁止用 `float64` 或把 JSON number 直接交给 `decimal.Decimal`。规范化输出的 Decimal JSON 必须带引号。

### `MarketSource`

实现的共享接口必须逐字保持以下方法集合，不增加第五个方法：

```go
type MarketSource interface {
	SnapshotInstruments(ctx context.Context, marketType MarketType) ([]InstrumentMetadata, error)
	FetchCandlePage(ctx context.Context, request CandlePageRequest) (CandlePage, error)
	SubscribeCandles(ctx context.Context, instrument Instrument, interval CandleInterval, handle CandleHandler) error
	SubscribeTickers(ctx context.Context, instrument Instrument, handle TickerHandler) error
}
```

输入输出契约：

| 能力 | 输入 | 输出 |
| --- | --- | --- |
| 元数据快照 | `Context`、`spot` 或 `usdt_perpetual` | 完整、按 `NativeSymbol` 升序且无重复的 `[]InstrumentMetadata`；任一项畸形则整体错误 |
| 历史分页 | 已持久化 Instrument、interval、UTC `[StartTime, EndTime)`、`Limit 1..300`、可空 cursor | 严格升序 `CandlePage`；cursor 是下一根 OpenTime 的 UTC RFC3339Nano `Z` 字符串 |
| Candle 订阅 | Context、单个 Instrument、interval、非 nil handler | 阻塞并串行回调；取消清理后返回 Context cause；handler 错误原样返回 |
| Ticker 订阅 | Context、单个 Instrument、非 nil handler | 阻塞并串行回调；取消清理后返回 Context cause；handler 错误原样返回 |

`SourceError` 只允许 `invalid_request`、`rate_limited`、`unavailable`、`protocol`。只有 `rate_limited` 与 `unavailable` 可重试；Context 和 handler 错误不得包装。规范化错误不得包含原始 payload、Header、查询串、完整 URL 或任何凭据。

### 样本输入与验收输出

Binance 和 OKX 各提供现货元数据、USDT 永续元数据、两页历史 `1m` Candle、一个实时未闭合 Candle、一个 Ticker 共六个 JSON fixture。样本使用合成数值和固定 `2026-08-01` UTC 时间，只保留公共协议所需字段。

必须证明：

1. 两所现货与 USDT 线性永续都映射到同一类型和枚举，OKX 永续使用 `ctValCcy/settleCcy`。
2. 两页各形成 00:00、00:01、00:02 三根严格升序 Candle，第一页 cursor 为 `2026-08-01T00:02:00Z`，末页为空，无重无漏。
3. Binance 闭区间毫秒和 OKX open time 都归一为排他 CloseTime；实时 `k.x=false`/`confirm="0"` 都得到 `IsClosed=false`。
4. Ticker 时间为 UTC，last/bid/ask 全为正数且 bid 不大于 ask。
5. JSON number、指数、19 位小数、超 20 位整数、未知枚举、字段缺失和错误 OHLC/时间边界都失败，不返回部分结果。
6. fixture 驱动的订阅在首事件后取消或 handler 报错时按公共契约结束，返回后不再回调。
7. 标准 `ON CONFLICT` 写入相同样本两次后每个逻辑键仍只有一行，Instrument 保留首次 UUIDv7；Ticker 较旧事件不能覆盖较新快照。

生产代码只实现共享类型、校验、错误和两所纯载荷规范化函数。fixture 驱动源可以仅存在于 `_test.go`；不得为了样本建立回放框架、通用插件或真实客户端。

包边界固定为父包 `backend/internal/marketdata` 拥有共享契约，`binance`、`okx` 子包分别拥有原始 DTO 与纯规范化函数并只能导入父包；父包不得导入子包，三者不得导入 `internal/api`、`internal/service` 或 `internal/db`。

## PostgreSQL 契约

- 新文件只能是 `backend/internal/migration/sql/00003_a2_market_contract.sql`，同时包含 Goose Up/Down，保持默认事务。
- 三张表、列、类型、键和 Check 约束严格按公共契约实现；不增加 surrogate Candle/Ticker ID、raw JSON、审计列、触发器或二级时间索引。
- PostgreSQL 17 没有可依赖的 `uuidv7()` 默认值；ID 由 Go 生成，SQL 用 UUID 文本 version/variant 约束验证。
- Down 先按 `market_candles`、`market_ticker_snapshots`、`market_instruments` 顺序取得 `ACCESS EXCLUSIVE` 锁，再以临时 guard 校验总行数为零，最后子表到父表删除。
- migration 测试更新最新版本断言为 3，并把“数据库领先”伪版本改为 4；`cmd/migrate` 输出断言同步为 `current=3 latest=3 applied=3`。
- 保留既有测试意图并调整回滚步数：完整空库生命周期 Down 3 步；基线 Down 测试先移除 A2 与 observability 两步；observability Down 测试先单独移除空 A2；并发基线 Down 测试同样先回到 version 1。不得因版本增加而删除或弱化 A1 用例。
- 新测试至少覆盖列类型、UUIDv7、枚举、Decimal/OHLC/时间约束、外键、三类唯一键、重复标准 upsert、旧 Ticker 防覆盖、空表 Down/重放、任一新表非空 Down 原子保护。

## 文件所有权

### 允许修改

| 路径 | 唯一用途 |
| --- | --- |
| `backend/go.mod` | 增加冻结的两个直接依赖 |
| `backend/go.sum` | 上述依赖校验和 |
| `backend/internal/marketdata/**` | 共享契约、纯规范化函数、测试与脱敏 fixture |
| `backend/internal/migration/sql/00003_a2_market_contract.sql` | A2 三表 Up/Down |
| `backend/internal/migration/migrations_test.go` | migration 版本与 A2 schema 契约测试 |
| `backend/cmd/migrate/main_test.go` | 最新 migration 数量断言 |
| `docs/runbooks/database-migrations.md` | 只同步 `00003`、三表和 A2 回滚/验证说明 |

### 禁止修改

- `docs/contracts/README.md`、本任务卡、`docs/roadmap/implementation-plan.md` 和全部 ADR；这些是 Sol 已冻结输入。
- `backend/internal/migration/sql/00001_a1_postgres_baseline.sql`、`00002_a1_observability.sql`。
- `backend/internal/db/**`、`backend/internal/service/**`、`backend/internal/api/**`、`backend/internal/config/**`、`backend/main.go`、`backend/cmd/migrate/main.go`。
- `.github/**`、`scripts/**`、`docker-compose.yml`、`deploy/**`、`frontend/**`、`worker/**`、`.codegraph/**`。
- 任何真实网络客户端、真实交易所响应转储、私有接口、凭据、生产配置、生成目录或额外依赖。

若实现必须越过允许路径，先停止并回流 Sol，不得用“顺手修复”扩大 diff。

## 唯一一次适用验收

前置条件：从 `backend` 目录运行；`COINSPHERE_TEST_POSTGRES_DSN` 只能使用用户已指定的可丢弃 PostgreSQL 17/TimescaleDB 测试库。本机没有 Docker/WSL，不得自行推断或连接 dev/uat/prod。实现和格式整理全部完成后只运行一次：

```powershell
& 'C:\Users\LTS\go\go1.26.5\bin\go.exe' test -count=1 ./internal/db ./internal/marketdata/... ./internal/migration ./internal/service ./cmd/migrate
```

若环境变量未配置，现有 PostgreSQL 测试会在非 CI 环境跳过；必须如实记录“数据库契约未在本机执行”，不得改用生产 DSN、伪造成功或反复运行其他门禁。格式、Vet、Staticcheck、全量构建和固定 TimescaleDB 验证交给该 PR 的一次 GitHub 快速层。

## 回滚

代码回滚与 schema 回滚分开：

1. 回退实现时 revert A2 实现提交，默认保留 schema 与 `schema_migrations`。
2. 确需回滚 `00003` 时，先停止所有未来会写三张表的进程，记录当前版本并验证备份；只执行 `go run ./cmd/migrate -config ./config.yml -direction down -steps 1`。
3. 只有三张 A2 表都为空时 Down 才成功。任一表非空或 Down 失败时立即停止，保留表、数据和版本记录；禁止删除行或手工修改 `schema_migrations`，需要降级只能保留兼容 schema 或从已验证备份恢复。

## 契约缺口回流 Sol

出现以下任一情况，Terra 必须停止公共契约相关修改，保留证据并回流 `gpt-5.6-sol max`：

- Binance/OKX 的公开字段无法无损映射，必须新增或改变领域字段、枚举、interval、JSON、Decimal、UTC 或 UUID 语义。
- 四个 `MarketSource` 方法不足，必须加入批量订阅、能力发现、额外生命周期方法、不同 cursor 或新的错误分类。
- 三表 schema 无法满足样本，必须增加表、列、索引、nullable/raw payload、Timescale hypertable、触发器或改变唯一键/Down 语义。
- 实现需要第三个依赖、允许列表外路径、真实网络连接、共享 runner、Store/Repository、API 或 UI。
- 冻结样本之间相互矛盾，或正确实现无法同时通过 Decimal/UTC、分页、取消和 PostgreSQL 约束。

普通编译错误、fixture DTO 字段修正、约束 SQL 语法、测试辅助代码和允许路径内的最小重排不属于设计缺口，由 Terra 直接修复。
