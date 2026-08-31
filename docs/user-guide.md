# CoinSphere 使用手册

本文面向 CoinSphere 个人自托管用户和管理员。当前可用能力是登录、RBAC、系统管理、系统监控、结构化系统日志、可信本地插件维护，以及工作流、Connector/AI、Binance 公共行情、可信 Go 策略、回测、Paper 和多渠道通知。

## 1. 使用范围

- 只支持 PostgreSQL 16。
- 不开放公开注册；首次启动创建内置超级管理员。
- 默认部署不包含 Python Worker、Private Executor、Redis 或消息代理。
- 当前版本不会连接 Binance 私有 API，不提供 Testnet/Live 放行或真实交易。

### 智能助手

超级管理员可从顶部栏的助手图标打开平台智能助手。首次使用前，在“系统管理 / 模型配置”添加并启用一个 OpenAI-compatible 模型；API Key 可留空，编辑时留空会保留当前密钥。

助手可以解释平台功能、查询工作流、运行、人工任务、日志、通知及插件领域摘要，也可以根据描述生成工作流方案。工具执行期间界面只显示工具名称和状态。工作流方案卡会列出节点、连线和待补密钥；点击“创建草稿”后只创建 `inactive` 工作流，随后从卡片进入编辑器补充密钥、检查配置并手工激活。助手不会自动运行工作流或执行真实交易。

会话历史按当前用户保存。删除会话会同时删除其消息；已经由方案创建的工作流不会随会话删除。普通用户不显示助手入口，也不能调用助手或模型配置接口。

## 2. Docker Compose 安装

需要 Docker Engine 或 Docker Desktop，并支持 Compose v2。先生成并长期保存至少 32 字节随机签名密钥。

Linux/macOS：

```bash
export COINSPHERE_AUTH__SECRET_KEY="$(openssl rand -hex 32)"
docker compose up -d --build
docker compose ps
```

PowerShell：

```powershell
$bytes = New-Object byte[] 32
[Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
$env:COINSPHERE_AUTH__SECRET_KEY = [BitConverter]::ToString($bytes).Replace('-', '').ToLowerInvariant()
docker compose up -d --build
docker compose ps
```

本地 Compose 依次启动 `postgresql`、一次性 `migrate`、`backend` 和 `web`。浏览器打开 <http://localhost:8080>。

停止服务不会删除数据：

```bash
docker compose down
```

不要把 `down -v` 当作停止或升级命令；它会删除 CoinSphere 数据卷。

## 3. 首次登录

初始超级管理员为 `coinsphere` / `coinsphere`。首次登录后立即在用户管理中修改密码，并完成以下检查：

1. 首页显示 Backend 和 PostgreSQL 正常。
2. 用户、角色和菜单管理页可打开。
3. 普通角色只获得需要的菜单和按钮权限。
4. 重启后原 `COINSPHERE_AUTH__SECRET_KEY` 仍保持一致。

系统不提供匿名注册。Access Token 只保存在当前前端会话内；退出登录会撤销当前令牌。

## 4. 当前页面

| 页面       | 用途                                                       |
| ---------- | ---------------------------------------------------------- |
| 首页       | 查看 Go 进程、HTTP、PostgreSQL、migration 和插件版本状态   |
| 节点定义   | 查看核心与编译期插件提供的可用节点                         |
| 工作流定义 | 使用画布创建、编辑、启停和手工运行工作流                   |
| 工作流日志 | 搜索每次 Run，查看节点尝试、多行日志、摘要和制品引用        |
| 币种数据   | 查看工作流采集的真实交易标的目录并进入对应工作流           |
| K 线详情   | 使用 `official.quant` 独立页面，以原形式查看闭合 K 线      |
| 插件管理   | 查看编译进当前应用的插件、注册节点及节点配置参数           |
| 系统日志   | 按级别、组件、请求和用户查询结构化运行日志                 |
| 系统管理   | 维护用户、角色、菜单、权限码与出站代理                     |

导航和页面布局沿用 `/scheduler/*`、`/data/*` 形态，底层工作流、修订、Run 和插件接口统一使用当前 `/api/v1` 契约。旧交易、新闻和模型配置接口未恢复，因此对应菜单不展示。

## 5. 工作流定义

只有超级管理员可以访问工作流定义页面和 `/api/v1/workflows`。列表只显示名称、版本、`已激活/未激活/异常`、创建时间和操作，不再显示执行数、运行态抽屉或归档。每行“工作流日志”打开该工作流的 Run 搜索；编辑器按核心与编译期插件下发的 JSON Schema/UI Schema 展示节点配置，保存后生成新修订，手工工作流可从列表立即运行。

保存完整校验通过后才创建新修订。密钥输入只表示替换，留空保持原值；系统只显示是否已配置。同一节点实例的类型和版本不变时保留状态。切换到历史修订时工作台只读。

激活后，手工工作流可用“手动运行”创建固定当前修订的 Run；定时工作流可选择 `everySeconds` 间隔，或配置六段 Cron 和 IANA 时区。服务恢复后最多补一次错过的运行，再计算下一未来时刻。事件按 `partitionkey` 保证同工作流同分区顺序，不同分区可并行。失败节点默认重试三次并从最后成功检查点继续。停用会取消连续流 Trigger，并在当前 Action 完成后停止领取新 Run；待处理 Run 保留，重新激活后续跑。连续流异常进入“异常”后，先停用恢复为“未激活”，确认修复再重新激活。

实时运行日志按每个节点 attempt 展示开始、业务和结束记录：开始记录包含尝试次数、Loop 轮次和脱敏输入，结束记录包含状态、耗时、脱敏输出或受控错误。节点详情只保留状态、耗时、错误和输入输出摘要，不重复展示业务日志。标题栏“历史日志”默认查询最近 24 小时，可按 UTC 时间、运行状态、触发方式和关键词搜索；一行是一条完整 Run，进入详情后固定该 Run，不跟随最新流式运行。详情页通过 `coinsphere.workflow-runs.v1` WebSocket 接收轻量更新通知，再从 HTTP API 刷新持久事实；断线不会改变运行结果。日志不包含原始载荷或密钥，终态历史默认保留 30 天。

`core.human_approval` 进入等待后释放执行与分区容量。批准、拒绝、过期或被相同业务键的新任务取代后，原 Run 从持久检查点继续。每个任务只能决定一次。

`core.loop` 使用内嵌 DAG，必须配置正数迭代上限、绝对超时和 Boolean CEL 退出条件。工作台新建 Loop 时会提供最小 `loop_item -> loop_end` 子图；人工等待节点不能放入 Loop。

### 5.1 事件与 Webhook

超级管理员可向 `POST /api/v1/events` 发布 CloudEvents 1.0 结构化 JSON。事件必须包含 UTC `time`、对象 `data` 和字符串扩展属性 `partitionkey`；相同 `(source,id)` 只能对应相同内容。

Webhook URL 为 `/api/v1/webhooks/{workflowId}`，无需登录，但必须同时提供 `X-CoinSphere-Webhook-Secret`、`Idempotency-Key` 和 `X-CoinSphere-Partition-Key`。正文是不超过 1 MiB 的 JSON 对象。Webhook Secret 在节点密钥栏配置，保存后不可读取；未配置密钥的工作流不能启动。

### 5.2 Connector 与 AI

Connector HTTP、Connector WebSocket 和 AI 模型调用默认不能访问任何外部域名。启动前在环境中配置精确公共域名数组，例如：

```bash
COINSPHERE_WORKFLOW__HTTP_ALLOWED_HOSTS='[api.example.com,models.example.com]'
```

不支持通配符、IP、自动包含子域或私网目标。环境代理不会被使用，DNS 解析中出现任一非公网地址都会拒绝。Binance 域名只开放明确的公共 GET/公共 WebSocket；不得配置授权请求、私有端点或真实交易密钥。AI 节点使用节点本地 API Key，要求 OpenAI-compatible JSON 响应，输入和输出都必须是对象。

具体请求、图格式和冲突语义见[公共契约](contracts/README.md)。工作台不要求管理员编辑原始 JSON。

### 5.3 Quant 与回测

服务器直连 Binance 受限时，先在“系统管理 / 代理配置”新增 HTTP 或 SOCKS5 代理并执行连接检测。密码只写入服务端加密存储，编辑时留空会保留原密码。代理仍被任一工作流历史修订引用时不能删除；需要下线时先停用，使用它的节点会明确失败。

应用首次启动且尚无币种同步节点时，会创建并激活“Binance 币种元数据采集”：立即运行一次，之后在北京时间 `00:00 / 06:00 / 12:00 / 18:00` 运行。`official.quant.sync_instruments` 可选择 Spot/USD-M、直连或一个已启用代理，并配置报价资产、基础资产和交易对的黑白名单；输入会转大写、去空格和去重，空白名单不限，所有白名单取交集，任一黑名单命中即排除。多个元数据工作流各自保存成功快照，币种目录显示全部来源并集；停用只停止调度并保留上次成功快照。采集状态和实时日志从“查看采集工作流”进入工作流画布查看。

Quant 只连接 Binance Spot 和 USD-M 公共 REST/WebSocket。先创建并激活 `quant-market-data`，在 `official.quant.realtime_candles` 中选择市场、代理、单个品种和一个或多个固定周期；同一 `market + instrument + proxyId` 使用一条 combined-stream WebSocket，并在连接内合并所需周期。断线只重连，不隐式补数。每根实时闭合 K 线保存一条 Quant 行情和 CloudEvent，并为命中的工作流创建一条包含完整节点路径的 Run；Run 不复制 OHLCV 正文。月线不属于当前固定周期集合。

缺口修复使用独立的 `official.quant.backfill_candles` Action，可接手动、定时或节点化回测入口，并可选择直连或已启用代理。普通运行会为单个品种的每个选中周期抓取指定 UTC 结束时间之前最近 N 根闭合 K 线；作为回测入口时先检查回测区间及前置根数，完整则直接进入回测，不足才补数。结束时间留空时使用执行时间，每周期最多 10000 根。补数只写入 K 线表并返回抓取/新增数量，不发布 `market.candle.closed`，重复执行由数据库主键去重。元数据节点只有在全部选中市场都抓取并解析成功后才替换快照；失败时目录保持不变，过滤后为空则保存为空快照。代理只供上述三类 Binance 公共行情节点显式选择，不会自动作用于 Connector、AI、通知、QQ、Paper 报价或其他 Quant 节点。

`quant-strategy` 消费 `market.candle.closed` 并调用已编译的 SMA crossover Go 策略。`quant-backtest` 读取已落库的闭合 K 线，在下一根 K 线开盘成交并应用 Decimal 手续费和滑点；日期必须是 UTC。运行结果中的 Quant 页面可查看品种、K 线、策略和回测摘要，并下载后校验完整明细 SHA-256。

量化判断拆分为放量、价格波动、MACD、KDJ、RSI 和布林带六种独立节点，每个节点只配置一种指标和一种规则，并独立选择市场、交易对、检查周期和 K 线周期。当前支持 N 根 K 线首尾上涨/下跌/绝对涨跌、期间振幅、MACD 金叉/死叉/零轴位置、KDJ 金叉/死叉及 K/D/J 阈值、Wilder RSI 阈值和布林带突破。节点只使用已经闭合且连续的 K 线；历史不足时走 false，不发送通知。

需要在 K 线页展示命中结果时，将指标判断节点的 `true` 分支连接到“输出信号”。市场、交易对、周期、名称、指标、命中时间、摘要和值由编辑器自动绑定，无需重复填写。每根命中的闭合 K 线都会生成一条普通行情信号；同一根 K 线的多个信号在图上合并标记，并在右侧逐条展示。普通行情信号不触发审批、Paper 下单或真实交易。

判断节点提供 `true / false` 两个出口，每个出口都可扇出、串联、汇合或连接普通节点。串联判断可表达跨节点 AND，并联汇入同一后继可表达 OR，false 出口可表达反向条件。连续命中仍会执行后续判断；直接把一个或多个判断节点连接到任一通知节点时，编辑器自动只在整条 true 路径刚刚成立时通知，并聚合本次命中的摘要。路径恢复为 false 后再次成立，会再次发送通知。

### 5.4 Paper 与通知

选择 `quant-paper` 会在一个事务内创建未激活的共享行情工作流和 Paper 策略工作流。先确认市场、品种、周期和五项风险限制，再分别激活两个工作流。默认 `core.human_approval` 和 `paper_execute` 都使用 `human`；自动模式必须同时显式改为 `auto`，且不能删除任一风险上限。

Paper 链路为“闭合 K 线 → 可信策略 → 信号 → 人工/自动决策 → 新鲜公共报价与风险复核 → Paper 账本 → 通知”。账户按工作流和 Paper 节点实例隔离。风险拒绝不创建账户、订单或部分账本；成交、费用和账本使用 Decimal 字符串与 UTC 时间，不连接 Binance 私有接口。

工作流可分别添加站内、钉钉和 SMTP 通知节点。站内目标支持用户和角色多选，角色在执行时展开为当前启用用户并与直接用户去重；旧节点未配置目标时发送给工作流创建者。钉钉可选择纯文本或 Markdown 及可选加签，SMTP 必须选择 TLS 或 STARTTLS。渠道凭据只在节点密钥输入中配置，留空保留已有密钥；部署和 CI 不配置或试发真实消息。

独立 `official.qq` 插件提供消息接收和发送节点。接收节点通过 QQ Gateway 订阅群聊 @ 消息与消息列表单聊，输出群或用户 OpenID、消息正文、回复所需消息 ID、引用索引和附件元数据；发送节点支持群聊或单聊的文本、Markdown，以及通过公网 URL 上传的图片、视频、语音和文件，并可使用键盘模板 ID 或回复消息 ID。QQ 插件只连接正式环境，同一 AppID 只能激活一个接收节点；启用前需在 QQ 开放平台开通 `GROUP_AND_C2C_EVENT` 并配置部署公网 IP 白名单。

顶部铃铛只显示当前用户的站内通知和未读数，支持单条或全部已读。登录后浏览器通过 `coinsphere.notifications.v1` WebSocket 接收实时更新，断线时仍可从持久列表恢复。

Paper 页面展示账户、持仓、最近脱敏 Run 和信号；移动端使用纵向信号队列。管理员可从系统 Quant 路由执行账户投影重建。具体恢复步骤与证据项见 [Paper 恢复与观察](runbooks/paper-recovery.md)。当前仍不提供在线策略源码、Testnet、Live、交易所私有凭据或真实下单。

## 6. 插件维护

插件会参与主 Go 进程和主 Vue 前端编译，只安装已经审查过的本地可信源码。维护前停止应用并备份数据库。

在 `backend` 目录先执行只读校验：

```powershell
go run ./cmd/coinsphere plugin validate D:\plugins\connector
```

校验通过后，在维护窗口使用：

```powershell
go run ./cmd/coinsphere plugin install --config ./config.yml --backend-root . D:\plugins\connector
go run ./cmd/coinsphere plugin upgrade --config ./config.yml --backend-root . D:\plugins\connector
go run ./cmd/coinsphere plugin uninstall --config ./config.yml --backend-root . official.connector
go run ./cmd/coinsphere plugin purge-data --config ./config.yml --backend-root . --confirm "PURGE official.connector" official.connector
```

- `install`/`upgrade` 执行插件 migration、生成注册表并构建 Backend/Web 镜像，但不启动候选镜像。
- 同 major 升级必须保留所有旧 migration 字节不变；major 升级当前拒绝。
- 有活动工作流、修订或结果视图引用时不能卸载。
- 卸载保留插件 schema；`purge-data` 只有在无任何活动或历史引用时才删除 schema。
- 同一 checkout 一次只运行一个插件维护命令。

插件目录、manifest、SDK、Vue 页面、migration 和契约测试见[插件开发指南](plugin-development.md)。

## 7. 数据、备份与升级

生产数据库使用服务器现有 PostgreSQL 16 的独立 `coinsphere_go` 数据库，文件与制品位于部署目录的 `data/backend`。升级前至少保存：

- PostgreSQL 一致性备份及恢复命令。
- 上传目录备份。
- `data/backend/artifacts` 制品目录备份；它必须和数据库快照属于同一恢复点。
- 当前应用版本、Compose 配置和镜像 digest。
- `COINSPHERE_AUTH__SECRET_KEY` 的安全离线副本。

升级使用最新固定版本镜像先运行 migration，再启动 Backend/Web。应用启动只校验 schema，不自动建表。失败时停止候选版本并按[发布 Runbook](runbooks/release.md)恢复匹配的应用镜像；不要手工改 migration 账本或自动执行 Down。

开始正式 Paper 观察前必须记录 migration freeze 提交；从该提交开始，已有 migration 只能保持字节不变并追加新版本。任何数据重置都必须按[数据库迁移 Runbook](runbooks/database-migrations.md)确认目标、备份和是否已有 Paper 观察证据。

## 8. 健康检查与排障

```bash
curl --fail http://127.0.0.1:8080/health/live
curl --fail http://127.0.0.1:8080/health/ready
docker compose ps
docker compose logs --tail=200 migrate backend web
```

| 现象                     | 优先检查                                                |
| ------------------------ | ------------------------------------------------------- |
| Compose 提示缺少签名密钥 | 当前环境是否提供稳定的 `COINSPHERE_AUTH__SECRET_KEY`    |
| `migrate` 失败           | PostgreSQL 网络、凭据、核心版本和目标数据库 schema      |
| Backend 不 ready         | PostgreSQL 网络、凭据、migration 是否落后或超前         |
| Web 打不开               | Web 端口、Backend 健康和反向代理配置                    |
| 登录立即失效             | 签名密钥是否变化、浏览器时间是否异常                    |
| 插件安装失败             | manifest、Core/SDK 版本、migration、Go/Vue 构建输出     |
| 插件无法卸载             | CLI 输出的活动引用，先在拥有模块解除引用                |
| 工作流返回 `409`         | 修订指针过期、状态冲突、事件内容冲突或积压达到上限      |
| 连续流变为“需处理”       | Trigger 配置、域名白名单、DNS、凭据或远端握手状态       |
| Binance 目标解析为非公网地址 | 在系统管理配置并检测服务器可用代理，再在采集节点选择该代理 |
| Paper 信号被拒绝         | 结果页拒绝原因、报价时效、数量步进、账户状态及五项上限  |
| Paper 投影不一致         | 先保留事实和备份，再按 Paper 恢复 Runbook 执行重建      |

日志和截图必须移除 DSN、Token、密码、原始载荷和个人数据。

## 9. 安全边界

- 不把真实 API Key、Secret、令牌、DSN 或生产配置提交到代码、日志、Issue、PR、CI 或 AI 上下文。
- 不安装不可信、远程下载或未经审查的插件源码。
- AI、工作流和通用 HTTP 节点不得调用交易所私有接口或绕过风控。
- 合并、发布、部署或开始 Paper 观察都不会自动启用 Testnet、Live 或任何真实交易。

## 10. 文档索引

- [当前架构](architecture/overview.md)
- [代码结构](code-structure.md)
- [插件开发指南](plugin-development.md)
- [公共契约](contracts/README.md)
- [质量门禁](quality/quality-gates.md)
- [本地开发](runbooks/development.md)
- [数据库迁移](runbooks/database-migrations.md)
- [Paper 恢复与观察](runbooks/paper-recovery.md)
- [发布与回滚](runbooks/release.md)
