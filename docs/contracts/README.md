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
- A1 登录与 refresh 响应只返回 `{"token":"..."}`，Access Token 默认有效期 15 分钟且只保存在浏览器内存中。Refresh Token 只存在名为 `coinsphere_refresh_token` 的 Cookie：`HttpOnly`、`SameSite=Strict`、`Path=/api/auth`，HTTPS 请求同时设置 `Secure`。
- `POST /api/auth/refresh` 和 `POST /api/auth/logout` 均无请求体。refresh 在 PostgreSQL 短事务中锁定旧记录、写入新记录并吊销旧记录；并发复用或已吊销令牌再次出现时吊销该用户全部 Refresh Token。logout 即使 Cookie 缺失或无效也清除浏览器 Cookie。
- 管理员密码恢复通过 `go run ./cmd/admin -username <name>` 执行，密码只从隐藏回显的标准输入读取；密码更新与该用户全部 Refresh Token 吊销在同一事务提交。

## A1 可观测性

- 每个 HTTP 响应包含 `X-Request-ID`。请求提供的值仅在匹配 `[A-Za-z0-9._-]{1,64}` 时传播，否则服务生成新值；应用日志和审计使用同一值。
- 匹配路由的 `POST`、`PUT`、`PATCH`、`DELETE` 请求写入 `audit_records`。记录只包含 Request ID、内部用户 ID、路由动作、无查询串资源路径、结果、HTTP 状态和 UTC 时间，不包含 Header、查询串、正文、令牌、凭据、错误正文、IP 或个人资料。
- 审计在业务处理结束后使用独立短事务；写入失败不会把已提交动作伪装成回滚或向客户端返回可重试结果，只增加固定错误日志与审计失败指标。
- `GET /health/live` 只检查进程存活；`GET /health/ready` 和兼容别名 `GET /health` 只检查 PostgreSQL 是否可访问。就绪失败返回 `503 {"status":"unavailable"}`，不得返回运行配置或驱动错误。
- `GET /metrics` 只暴露五个无标签指标：HTTP 请求总数、失败数、在途数、审计写失败数和进程运行秒数。该接口不承诺外部存储、聚合或告警能力。

## A2 行情最小契约

本节冻结 A2 首个纵向 PR 的实现规格。该 PR 只交付规范化领域类型、最小 PostgreSQL schema、`MarketSource`、纯载荷规范化代码和 Binance/OKX 脱敏可执行样本；不连接公网，不启动 collector，不提供行情 API，也不实现共享 runner 或具体 PostgreSQL 存储层。后续切片只能消费本节契约；需要增加字段、枚举、方法或表时必须先回到设计阶段。

### Decimal、UTC 与 UUIDv7

- Go 金融数值固定使用 `github.com/shopspring/decimal` v1.4.0 的 `decimal.Decimal`。原始 JSON DTO 先把价格、数量和成交量读为 `string`，再调用共享解析逻辑；禁止先进入 `float64`、未加引号的 JSON number 或科学计数法。
- 接受的十进制文本匹配 `^[0-9]+(?:\.[0-9]+)?$`，小数位最多 18 位、整数位最多 20 位。解析器允许零，字段不变量再决定必须大于零还是允许等于零。Go 边界必须在写库前拒绝超出 `numeric(38,18)` 的值，不能依赖 PostgreSQL 对多余小数位静默舍入。
- 规范化 JSON 中 Decimal 固定输出十进制字符串，使用 `decimal.Decimal.String()` 的无指数规范形式；不得设置全局 `decimal.MarshalJSONWithoutQuotes = true`。本 PR 不把领域结构体作为不可信 JSON 请求 DTO，交易所载荷只能先进入字段为字符串的原始 DTO。
- 所有 `time.Time` 必须经 `.UTC()` 规范化，`Location()` 为 `time.UTC`。外部毫秒时间戳使用 `time.UnixMilli(...).UTC()`；JSON 使用带 `Z` 的 RFC3339Nano；数据库使用 `timestamptz`。K 线日线以 UTC 零点切分，OKX 必须映射 `1Dutc`，不得使用交易所本地日线。
- 金融资源 ID 的 Go 类型固定为 `github.com/google/uuid` v1.6.0 的 `uuid.UUID`，新 Instrument 只用 `uuid.NewV7()` 生成。数据库使用 `uuid` 且校验 RFC 9562 version 7 与 RFC 4122 variant；没有数据库默认值。JSON 使用小写规范 UUID 字符串，`uuid.Nil` 不是合法持久化 ID。
- Candle 与 Ticker 是由复合逻辑键标识的行情事实，不额外创建 UUID 或 surrogate ID；它们只引用 Instrument 的 UUIDv7。

### 枚举与 Go 类型

以下名称、字符串值、字段类型和 JSON 字段名是冻结契约：

```go
type Venue string

const (
	VenueBinance Venue = "binance"
	VenueOKX     Venue = "okx"
)

type MarketType string

const (
	MarketTypeSpot          MarketType = "spot"
	MarketTypeUSDTPerpetual MarketType = "usdt_perpetual"
)

type InstrumentStatus string

const (
	InstrumentStatusTrading   InstrumentStatus = "trading"
	InstrumentStatusSuspended InstrumentStatus = "suspended"
)

type CandleInterval string

const (
	CandleInterval1m  CandleInterval = "1m"
	CandleInterval5m  CandleInterval = "5m"
	CandleInterval15m CandleInterval = "15m"
	CandleInterval1h  CandleInterval = "1h"
	CandleInterval4h  CandleInterval = "4h"
	CandleInterval1d  CandleInterval = "1d"
)

// InstrumentMetadata 是交易所元数据快照项；内部 ID 只能由持久化边界首次创建。
type InstrumentMetadata struct {
	Venue        Venue            `json:"venue"`
	MarketType   MarketType       `json:"marketType"`
	NativeSymbol string           `json:"nativeSymbol"`
	BaseAsset    string           `json:"baseAsset"`
	QuoteAsset   string           `json:"quoteAsset"`
	Status       InstrumentStatus `json:"status"`
	PriceTick    decimal.Decimal  `json:"priceTick"`
	QuantityStep decimal.Decimal  `json:"quantityStep"`
}

type Instrument struct {
	ID           uuid.UUID        `json:"id"`
	Venue        Venue            `json:"venue"`
	MarketType   MarketType       `json:"marketType"`
	NativeSymbol string           `json:"nativeSymbol"`
	BaseAsset    string           `json:"baseAsset"`
	QuoteAsset   string           `json:"quoteAsset"`
	Status       InstrumentStatus `json:"status"`
	PriceTick    decimal.Decimal  `json:"priceTick"`
	QuantityStep decimal.Decimal  `json:"quantityStep"`
}

type Candle struct {
	Venue        Venue           `json:"venue"`
	InstrumentID uuid.UUID       `json:"instrumentId"`
	Interval     CandleInterval  `json:"interval"`
	OpenTime     time.Time       `json:"openTime"`
	CloseTime    time.Time       `json:"closeTime"`
	Open         decimal.Decimal `json:"open"`
	High         decimal.Decimal `json:"high"`
	Low          decimal.Decimal `json:"low"`
	Close        decimal.Decimal `json:"close"`
	BaseVolume   decimal.Decimal `json:"baseVolume"`
	IsClosed     bool            `json:"isClosed"`
}

type Ticker struct {
	Venue        Venue           `json:"venue"`
	InstrumentID uuid.UUID       `json:"instrumentId"`
	OccurredAt   time.Time       `json:"occurredAt"`
	LastPrice    decimal.Decimal `json:"lastPrice"`
	BestBidPrice decimal.Decimal `json:"bestBidPrice"`
	BestAskPrice decimal.Decimal `json:"bestAskPrice"`
}
```

`InstrumentMetadata` 解决外部 symbol 与内部 UUID 的职责边界：交易所快照不能创建稳定 ID；具体 PostgreSQL 存储在 `(venue, market_type, native_symbol)` 首次插入时生成 UUIDv7，冲突更新只更新元数据且永不替换原 ID，再把完整 `Instrument` 传给历史和订阅方法。禁止让适配器缓存、推导或每次重新生成 Instrument ID。

### 类型不变量与交易所映射

- `Venue`、`MarketType`、`InstrumentStatus` 和 `CandleInterval` 只接受上述值；未知外部枚举返回 `protocol` 错误，不能静默映射为零值。
- `NativeSymbol` 保留交易所原始公开 symbol 并匹配 `^[A-Z0-9][A-Z0-9._-]*$`；`BaseAsset`、`QuoteAsset` 使用匹配同一字符集的大写资产代码。三者均无边界空白且非空，不在领域类型中保留交易所原始枚举、精度位数、quote volume 或其他单所字段。
- 现货只接受 Binance spot 与 OKX `SPOT`。USDT 永续只接受 Binance USDT-M `PERPETUAL` 和 OKX `SWAP` 且 `ctType=linear`、`settleCcy=USDT`；OKX 永续的 `BaseAsset=ctValCcy`、`QuoteAsset=settleCcy`。其他币本位、交割合约、USDC 合约和期权不进入快照。
- Binance spot 的 `TRADING` 映射为 `trading`，`PRE_TRADING`、`POST_TRADING`、`END_OF_DAY`、`HALT`、`AUCTION_MATCH`、`BREAK` 映射为 `suspended`；Binance USDT-M 的 `TRADING` 映射为 `trading`，`PENDING_TRADING`、`PRE_DELIVERING`、`DELIVERING`、`DELIVERED`、`PRE_SETTLE`、`SETTLING`、`CLOSE` 映射为 `suspended`。OKX 的 `live` 映射为 `trading`，`suspend`、`preopen`、`test` 映射为 `suspended`。未知状态使整个快照失败，禁止返回部分成功。
- `PriceTick`、`QuantityStep`、OHLC、`LastPrice`、`BestBidPrice`、`BestAskPrice` 必须大于零；`BaseVolume` 可以为零但不能为负。所有值还必须满足 `numeric(38,18)` 边界。
- Instrument 的 ID 必须是 UUIDv7。Candle/Ticker 的 `Venue` 与调用请求的 Instrument 一致，`InstrumentID` 原样复制请求 ID。
- Candle 的 `OpenTime` 必须按 Unix epoch 对齐到 interval；`CloseTime` 是排他的下一根边界并严格等于 `OpenTime + interval`。Binance 原始闭区间毫秒 `closeTime` 必须加 1 ms 后验证；OKX 由 `OpenTime + interval` 推导。
- Candle 满足 `Low <= min(Open, Close)`、`High >= max(Open, Close)`。历史样本固定 `IsClosed=true`；实时 Binance `k.x` 与 OKX `confirm` 分别映射 `IsClosed`，同一未闭合唯一键允许出现多次更新。
- Ticker 的 `OccurredAt` 非零且为 UTC，`BestBidPrice <= BestAskPrice`。不要求最近成交价位于当前买卖价之间。

Interval 映射固定如下：

| 规范值 | 时长 | Binance | OKX |
| --- | --- | --- | --- |
| `1m` | 1 分钟 | `1m` | `1m` |
| `5m` | 5 分钟 | `5m` | `5m` |
| `15m` | 15 分钟 | `15m` | `15m` |
| `1h` | 1 小时 | `1h` | `1H` |
| `4h` | 4 小时 | `4h` | `4H` |
| `1d` | 24 小时 UTC | `1d` | `1Dutc` |

### PostgreSQL 最小 schema

新增 migration 固定为 `backend/internal/migration/sql/00003_a2_market_contract.sql`，不得修改 `00001` 或 `00002`。本次只建立普通 PostgreSQL 表；不创建 Timescale hypertable、分区、保留策略、连续聚合、触发器、视图或 Repository 接口。

`market_instruments`：

| 列 | 类型与空值 |
| --- | --- |
| `id` | `uuid NOT NULL`，主键，无默认值 |
| `venue` | `varchar(16) NOT NULL` |
| `market_type` | `varchar(32) NOT NULL` |
| `native_symbol` | `varchar(64) NOT NULL` |
| `base_asset` | `varchar(32) NOT NULL` |
| `quote_asset` | `varchar(32) NOT NULL` |
| `status` | `varchar(16) NOT NULL` |
| `price_tick` | `numeric(38,18) NOT NULL` |
| `quantity_step` | `numeric(38,18) NOT NULL` |

- 主键为 `id`；`uq_market_instruments_natural_key` 唯一约束为 `(venue, market_type, native_symbol)`。
- `uq_market_instruments_venue_id` 唯一约束为 `(venue, id)`，只用于子表复合外键确保 Venue 与 Instrument 匹配。
- Check 约束名称和语义固定为：`ck_market_instruments_id_uuidv7` 校验 `id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'`；`ck_market_instruments_venue`、`ck_market_instruments_market_type`、`ck_market_instruments_status` 校验各自枚举；`ck_market_instruments_native_symbol` 校验 `^[A-Z0-9][A-Z0-9._-]*$`；`ck_market_instruments_base_asset`、`ck_market_instruments_quote_asset` 校验同一字符集且值等于大写形式；`ck_market_instruments_steps` 校验两个步长都大于零。

`market_candles`：

| 列 | 类型与空值 |
| --- | --- |
| `venue` | `varchar(16) NOT NULL` |
| `instrument_id` | `uuid NOT NULL` |
| `interval_code` | `varchar(4) NOT NULL` |
| `open_time` | `timestamptz NOT NULL` |
| `close_time` | `timestamptz NOT NULL` |
| `open_price`、`high_price`、`low_price`、`close_price` | 各为 `numeric(38,18) NOT NULL` |
| `base_volume` | `numeric(38,18) NOT NULL` |
| `is_closed` | `boolean NOT NULL` |

- 主键固定为 `(venue, instrument_id, interval_code, open_time)`；不再创建覆盖相同前缀的重复索引。
- `fk_market_candles_instrument` 以 `(venue, instrument_id)` 引用 `market_instruments(venue, id) ON DELETE RESTRICT`。
- Check 约束名称和语义固定为：`ck_market_candles_venue`、`ck_market_candles_interval` 校验枚举；`ck_market_candles_time` 校验两个时间有限、`open_time` 以 Unix epoch 为原点按 interval `date_bin` 对齐且 `close_time=open_time+interval`；`ck_market_candles_prices` 校验 OHLC 全部大于零；`ck_market_candles_volume` 校验基础成交量非负；`ck_market_candles_ohlc` 校验 Low/High 关系。

`market_ticker_snapshots`：

| 列 | 类型与空值 |
| --- | --- |
| `venue` | `varchar(16) NOT NULL` |
| `instrument_id` | `uuid NOT NULL` |
| `occurred_at` | `timestamptz NOT NULL` |
| `last_price`、`best_bid_price`、`best_ask_price` | 各为 `numeric(38,18) NOT NULL` |

- 主键固定为 `(venue, instrument_id)`；首轮只保留最新快照，不建立 Ticker 历史表或时间索引。
- `fk_market_ticker_snapshots_instrument` 以 `(venue, instrument_id)` 引用 `market_instruments(venue, id) ON DELETE RESTRICT`。
- Check 约束名称和语义固定为：`ck_market_ticker_snapshots_venue` 校验 Venue；`ck_market_ticker_snapshots_time` 校验事件时间有限；`ck_market_ticker_snapshots_prices` 校验三个价格都大于零；`ck_market_ticker_snapshots_spread` 校验 `best_bid_price <= best_ask_price`。

自动生成的 `market_instruments_pkey`、`market_candles_pkey`、`market_ticker_snapshots_pkey` 与两个命名唯一约束是本次仅有的索引。后续具体存储必须用冲突更新实现幂等：Instrument 冲突时保留首次 UUIDv7；Candle 重复逻辑键保持一行；Ticker 只在新 `occurred_at` 不早于当前快照时覆盖。本契约 PR 仅用迁移测试执行这些标准冲突语句，不提前交付具体存储对象。

Down 必须在同一 migration 事务中按子表到父表顺序对 `market_candles`、`market_ticker_snapshots`、`market_instruments` 取得 `ACCESS EXCLUSIVE` 锁，再统计三表总行数。只有总数为零才能依次删除子表和父表；任一表非空时 guard 失败，三张表、数据和 migration version 均原子保留。

### `MarketSource`

分页、错误和订阅辅助类型及接口签名固定如下：

```go
type CandleCursor string

type CandlePageRequest struct {
	Instrument Instrument     `json:"instrument"`
	Interval   CandleInterval `json:"interval"`
	StartTime  time.Time      `json:"startTime"`
	EndTime    time.Time      `json:"endTime"`
	Limit      int            `json:"limit"`
	Cursor     CandleCursor   `json:"cursor"`
}

type CandlePage struct {
	Candles    []Candle    `json:"candles"`
	NextCursor CandleCursor `json:"nextCursor"`
}

type CandleHandler func(Candle) error
type TickerHandler func(Ticker) error

type MarketSource interface {
	SnapshotInstruments(ctx context.Context, marketType MarketType) ([]InstrumentMetadata, error)
	FetchCandlePage(ctx context.Context, request CandlePageRequest) (CandlePage, error)
	SubscribeCandles(ctx context.Context, instrument Instrument, interval CandleInterval, handle CandleHandler) error
	SubscribeTickers(ctx context.Context, instrument Instrument, handle TickerHandler) error
}
```

`MarketSource` 不是动态插件点，不得增加 `Name`、`Capabilities`、生命周期 hook、泛型事件、原始 payload 或存储方法。Binance 和 OKX 的具体源在后续切片实现；本 PR 的生产代码只提供共享契约与两所纯载荷规范化函数。

包依赖固定为 `backend/internal/marketdata` 拥有公共类型、校验、Decimal 解析、错误和接口；`backend/internal/marketdata/binance` 与 `backend/internal/marketdata/okx` 只能向内依赖父包并各自拥有原始 DTO/纯规范化函数。父包不得反向导入子包，三者都不得依赖 `internal/api`、`internal/service` 或 `internal/db`，也不创建无归属的 `common` 包。

分页语义：

- `StartTime` 含、`EndTime` 不含，二者必须为 UTC、按 interval 对齐且 `StartTime < EndTime`；`Limit` 固定在 `1..300`。
- 首次请求的 `Cursor` 为空。非空 `CandleCursor` 是下一根待请求 `OpenTime` 的 UTC RFC3339Nano `Z` 字符串，必须位于原窗口内且按同一 interval 对齐；除 `Cursor` 外重放请求不得改变其他字段。
- `Candles` 按 `OpenTime` 严格升序、逻辑键不重复且全部位于窗口内。`NextCursor` 非空时严格前进；没有更多数据时为空。空页必须返回空 cursor，禁止制造无限分页。
- Binance 与 OKX 原始响应即使顺序不同，也必须产生上述统一顺序。游标只负责一次历史拉取的确定前进；长期断点、缺口修复和重试由后续共享 runner 负责。

订阅语义：

- 两个订阅方法都是阻塞调用，每次只订阅一个已持久化 Instrument；它们串行调用 handler，以 handler 的阻塞形成显式背压，不在源内排队、丢弃或并发回调。
- `ctx` 是唯一生命周期控制。取消时源先停止读取并释放自身资源，等待自有 goroutine 退出，再返回 `context.Cause(ctx)`；返回后不得再调用 handler。
- handler 返回错误时立即停止并原样返回该错误。源不得重试、退避、写库或记录重复日志；连接重建、限频、断点、持久化和日志属于后续共享 runner。
- 建连或读取失败返回下述分类错误。正常的实时 Candle 可以为同一逻辑键发送多次更新，Ticker 也不在适配器内去重。

### 错误分类

```go
type SourceErrorKind string

const (
	SourceErrorInvalidRequest SourceErrorKind = "invalid_request"
	SourceErrorRateLimited     SourceErrorKind = "rate_limited"
	SourceErrorUnavailable     SourceErrorKind = "unavailable"
	SourceErrorProtocol        SourceErrorKind = "protocol"
)

type SourceError struct {
	Kind       SourceErrorKind
	RetryAfter time.Duration
	Err        error
}
```

`SourceError` 必须实现 `error`、`Unwrap() error` 和 `Retryable() bool`。只有 `rate_limited`、`unavailable` 可重试；`RetryAfter` 只对 rate limit 有意义且不能为负。已知无效参数/不支持 symbol 是 `invalid_request`，429 或交易所限频码是 `rate_limited`，网络中断和 5xx 是 `unavailable`，非成功但无法识别的响应码、畸形 JSON、字段缺失、未知枚举和不变量失败是 `protocol`。错误不得包含响应原文、请求 Header、查询串、凭据或完整 URL。

调用 Context 的取消与截止错误不包装为 `SourceError`，必须保持 `errors.Is(err, context.Canceled|context.DeadlineExceeded)`；handler 错误也原样返回。源不决定重试次数。

### Binance/OKX 脱敏可执行样本

样本只保留公开协议的最小字段，使用合成价格和固定 UTC 时间，不包含 Header、URL、账户、密钥、签名、限频标识或原始错误正文。两个目录各固定六个输入文件：

```text
backend/internal/marketdata/binance/testdata/
  instruments_spot.json
  instruments_usdt_perpetual.json
  candles_1m_page_1.json
  candles_1m_page_2.json
  candle_1m_event.json
  ticker_event.json
backend/internal/marketdata/okx/testdata/
  instruments_spot.json
  instruments_usdt_perpetual.json
  candles_1m_page_1.json
  candles_1m_page_2.json
  candle_1m_event.json
  ticker_event.json
```

固定 Instrument ID 分别为 Binance spot `019c2f6d-7c00-7000-8000-000000000001`、OKX spot `019c2f6d-7c00-7000-8000-000000000002`、Binance USDT 永续 `019c2f6d-7c00-7000-8000-000000000003`、OKX USDT 永续 `019c2f6d-7c00-7000-8000-000000000004`。它们由测试的标准首次 upsert 提供，不写入元数据原始 JSON。

Metadata 样本分别保留 Binance exchange info 的 `PRICE_FILTER.tickSize`/`LOT_SIZE.stepSize` 与 OKX public instruments 的 `tickSz`/`lotSz`；OKX USDT 永续同时保留 `ctType`、`ctValCcy`、`settleCcy`。两所 spot 的规范化 `PriceTick="0.1"`、`QuantityStep="0.001"`，USDT 永续为 `"0.1"`、`"0.01"`，资产均为 BTC/USDT 且状态为 `trading`；NativeSymbol 分别保留 `BTCUSDT`、`BTC-USDT`、`BTCUSDT`、`BTC-USDT-SWAP`。

历史请求固定为 interval `1m`、窗口 `[2026-08-01T00:00:00Z, 2026-08-01T00:03:00Z)`、`Limit=2`。第一页包含前两根并返回 `2026-08-01T00:02:00Z`，第二页使用该 cursor 并包含最后一根后结束；Binance Kline 数组按升序存放，OKX history-candles 数组故意按降序存放以验证归一排序。原始时间统一使用对应 Unix 毫秒 `1785542400000`、`1785542460000`、`1785542520000`；Binance close time 分别为 `1785542459999`、`1785542519999`、`1785542579999`，OKX 只提供 open time 与 `confirm="1"`。

| OpenTime | Open | High | Low | Close | BaseVolume | CloseTime | IsClosed |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `2026-08-01T00:00:00Z` | `100.1` | `101.2` | `99.9` | `100.8` | `1.25` | `2026-08-01T00:01:00Z` | `true` |
| `2026-08-01T00:01:00Z` | `100.8` | `102` | `100.5` | `101.5` | `2.5` | `2026-08-01T00:02:00Z` | `true` |
| `2026-08-01T00:02:00Z` | `101.5` | `101.8` | `100.7` | `101` | `0.75` | `2026-08-01T00:03:00Z` | `true` |

实时 Candle 的原始 open time 为 `1785542580000`，规范化值固定为 Open/High/Low/Close/BaseVolume `101`/`102.2`/`100.9`/`102`/`0.5`、OpenTime `2026-08-01T00:03:00Z`、CloseTime `2026-08-01T00:04:00Z`、`IsClosed=false`；Binance 使用 kline 事件的 `k.t/k.T/k.o/k.h/k.l/k.c/k.v/k.x`，其中原始 close time 为 `1785542639999`，OKX 使用 `candle1m` 数据数组与 `confirm="0"`。Ticker 原始事件时间为 `1785542610000`，规范化为 `2026-08-01T00:03:30Z`，Last/BestBid/BestAsk 固定为 `102`/`101.9`/`102.1`；Binance 固定使用 `24hrTicker` 的 `E/c/b/a`，OKX 使用 `tickers` 的 `ts/last/bidPx/askPx`，禁止用缺少 last price 的 Binance `bookTicker` 代替。

最小验收矩阵：

| 检查 | Binance 输入 | OKX 输入 | 必须得到的规范化结果 |
| --- | --- | --- | --- |
| 现货元数据 | `BTCUSDT` | `BTC-USDT` | `spot`、BTC/USDT、`trading`，Decimal 步长无浮点中转 |
| USDT 永续元数据 | `BTCUSDT`/`PERPETUAL` | `BTC-USDT-SWAP`/linear/USDT | `usdt_perpetual`、BTC/USDT，单所字段不泄漏 |
| 历史分页 | 两页 Kline 数组 | 两页 candle 数组 | 三根严格升序、00:00/00:01/00:02 无重无漏，close time 为排他边界，末页 cursor 为空 |
| 实时 Candle | `k.x=false` | `confirm="0"` | 相同 OHLC/基础成交量语义、`IsClosed=false`、00:04 排他 close time |
| 实时 Ticker | 事件时间、last/bid/ask 字符串 | `ts`、`last`/`bidPx`/`askPx` 字符串 | UTC `OccurredAt`、正数价格、bid 不大于 ask |
| JSON/Decimal 负例 | 数值 token、指数、19 位小数、超 20 位整数 | 同左 | 均在边界失败；规范化 JSON 的 Decimal 均带引号 |
| 枚举/结构负例 | 未知状态、缺 filter、错误 close time | 未知状态、非 linear/USDT、缺字段 | 返回不可重试 `protocol`，不返回部分结果 |
| Context/handler | fixture 流首事件后取消或 handler 报错 | 同左 | 取消错误可用 `errors.Is` 识别，handler 错误原样返回，返回后零回调 |
| 数据库键与 Down | 两所规范化结果 | 两所规范化结果 | UUIDv7、三类唯一键、重复标准 upsert 仍各一行；任一新表非空时 Down 原子失败 |

这些样本是协议适配的可执行输入，不是网络客户端、回放服务或推荐数据集。缺口检测、补数调度、批量连接、质量报告、数据保留、衍生统计和 UI 查询模型继续留在后续 A2 切片。

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

浏览器握手必须携带唯一且合法的 `Origin`，其有效 scheme、主机和端口必须与请求完全同源；缺失、畸形、跨 scheme/主机/端口均拒绝。通知连接必须按顺序提供 `Sec-WebSocket-Protocol: coinsphere.notifications.v1, <access-token>`，服务端只回显固定协议 `coinsphere.notifications.v1`；缺失、顺序错误、额外协议或任何查询串均拒绝。开发和生产反向代理始终必须保留原始 Host（含非默认端口）及合法的有效 scheme，且不得记录令牌或事件 payload。

## 工作流 HTTP 外呼

- `http.request` 只允许访问 `workflow.http_allowed_hosts` 中配置的精确域名；配置项不接受通配符、端口或 IP，空列表表示禁止全部外呼。
- URL 只允许绝对 `http`/`https` 地址且不得包含 userinfo。首次校验、每次重定向和实际拨号都会解析域名；任一解析结果不是公网地址时整次请求被永久拒绝。
- 拨号只使用当次重新解析并校验过的 IP，不使用环境代理或连接复用。`Authorization`、`Cookie`、`Proxy-Authorization` 及名称含 key/token/secret/credential 的请求头不得发出。

## 异步任务

任务状态固定为 `queued`、`claimed`、`running`、`cancelRequested`、`succeeded`、`failed`、`canceled`。正常路径为 `queued -> claimed -> running -> succeeded/failed`；取消可从活跃状态进入 `cancelRequested -> canceled`。每次认领递增 `attempt_count` 并生成新的唯一 `lease_id`，启动、心跳和终态写入必须同时匹配任务 ID、租约 ID、合法前态及未过期的数据库时间。

Worker 必须周期续租；租约一旦过期，旧 Worker 立即失去心跳和终态写权限。过期的 `claimed/running` 在 `attempt_count < max_attempts` 时清除旧租约并重新排队，否则进入 `failed`。心跳观察到 `cancelRequested` 后不得继续续租；该状态在租约过期或取消请求满 4 秒时直接进入 `canceled`，禁止重试。任务取消从请求提交到伪任务停止并进入 `canceled` 不得超过 5 秒，即使 Owner 在观察取消后、确认终态前崩溃也必须满足该时限。

## Outbox 投递租约

Outbox 认领必须在单条数据库语句中完成候选选择、批量状态更新和结果返回，禁止先查后改。每条 `pending` 事件仅在 `available_at` 已到且 `attempt_count < max_attempts` 时可进入 `claimed`；认领递增一次尝试次数，并由数据库生成新的唯一 `lease_id`、记录 Owner、认领时间和租约到期时间。

续租、`claimed -> processed` 和订阅失败释放都必须同时匹配事件 ID、`lease_id`、Owner、`attempt_count` 和未过期的数据库时间。租约过期后旧 Owner 立即失去续租与终态写权限；即使事件已经恢复并重新认领，旧 token 的写入也只能影响零行。订阅失败保留已消耗的尝试次数并清空旧租约：仍有次数时按数据库时间与 `retry_backoff_seconds` 回到 `pending`，最后一次失败或租约过期时进入 `dead_letter`，且 `processed_at` 与 `dead_lettered_at` 相同。当前 `failed` 只表示旧 dispatcher 的既有终态，新 dispatcher 不再写入该状态。

工作流成功、最终失败和 stale 耗尽时，execution 终态、attempt、两条标准事件及对应入口状态必须在同一短事务提交；任一事件插入失败时整体回滚。显式 `event.emit` 节点的权威业务动作就是单条 Outbox 插入，不把整张图或外部副作用包入长事务。未告警死信通过原子设置 `alerted_at` 由一个实例领取，告警日志只允许固定 Outbox ID、尝试次数和分类，不得包含 payload、metadata、Owner、token 或异常正文。该标记提供 at-most-once 日志去重，不等同于可靠外部告警。

## 浏览器内容安全

- 助手 Markdown 保持 `html=false`，渲染结果在写入 DOM 前由固定版本的 DOMPurify 按 Markdown 标签白名单净化；第三方图片源被移除。
- Mermaid 使用 `securityLevel=strict` 且禁用 HTML label 和交互绑定，生成的 SVG 与其他动态 SVG 都必须再次净化；脚本、事件属性、外部资源引用和可执行 CSS 引用不得进入 DOM。
- 生产 Nginx 的 CSP 仅允许同源脚本，禁止 object、跨源 frame 和页面被嵌入；WebSocket 访问日志只记录 URI，不记录查询串。

## 交易命令

所有订单意图必须携带稳定 `intentId` 和确定性 `clientOrderId`。订单状态未知时先对账，禁止通过无条件重试创建第二笔订单。
