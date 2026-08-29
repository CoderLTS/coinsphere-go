# CoinSphere 使用手册

本文面向 CoinSphere 个人自托管用户和管理员。当前可用能力是登录、RBAC、系统管理、系统监控、结构化系统日志、可信本地插件维护，以及工作流、Connector/AI、Binance 公共行情、可信 Go 策略、回测、Paper 和站内通知。

## 1. 使用范围

- 只支持 PostgreSQL 16。
- 不开放公开注册；首次启动创建内置超级管理员。
- 默认部署不包含 Python Worker、Private Executor、Redis 或消息代理。
- 当前版本不会连接 Binance 私有 API，不提供 Testnet/Live 放行或真实交易。

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
| 插件管理   | 查看编译进当前应用的插件版本与贡献                         |
| 系统日志   | 按级别、组件、请求和用户查询结构化运行日志                 |
| 系统管理   | 维护用户、角色、菜单与权限码                               |

导航和页面布局沿用 `/scheduler/*`、`/data/*` 形态，底层工作流、修订、Run 和插件接口统一使用当前 `/api/v1` 契约。旧交易、新闻和模型配置接口未恢复，因此对应菜单不展示。

## 5. 工作流定义

只有超级管理员可以访问工作流定义页面和 `/api/v1/workflows`。列表只显示名称、版本、`已激活/未激活/异常`、创建时间和操作，不再显示执行数、运行态抽屉或归档。每行“工作流日志”打开该工作流的 Run 搜索；编辑器按核心与编译期插件下发的 JSON Schema/UI Schema 展示节点配置，保存后生成新修订，手工工作流可从列表立即运行。

保存完整校验通过后才创建新修订。密钥输入只表示替换，留空保持原值；系统只显示是否已配置。同一节点实例的类型和版本不变时保留状态。切换到历史修订时工作台只读。

激活后，手工工作流可用“手动运行”创建固定当前修订的 Run；定时工作流可选择 `everySeconds` 间隔，或配置六段 Cron 和 IANA 时区。服务恢复后最多补一次错过的运行，再计算下一未来时刻。事件按 `partitionkey` 保证同工作流同分区顺序，不同分区可并行。失败节点默认重试三次并从最后成功检查点继续。停用会取消连续流 Trigger，并在当前 Action 完成后停止领取新 Run；待处理 Run 保留，重新激活后续跑。连续流异常进入“异常”后，先停用恢复为“未激活”，确认修复再重新激活。

“工作流日志”默认查询最近 24 小时，可按 UTC 时间、运行状态、触发方式和关键词搜索。Run 详情复用工作流画布；选择节点可查看真实尝试、Loop 轮次、执行池、耗时、脱敏输入输出摘要和节点多行日志，总览显示事件摘要、结果和制品引用。详情页通过 `coinsphere.workflow-runs.v1` WebSocket 接收轻量更新通知，再从 HTTP API 刷新持久事实；断线不会改变运行结果。日志不包含原始载荷或密钥，终态历史默认保留 30 天。

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

应用首次启动且尚无币种同步节点时，会创建并激活“Binance 币种元数据采集”：立即运行一次，之后在北京时间 `00:00 / 06:00 / 12:00 / 18:00` 运行。`official.quant.sync_instruments` 可选择 Spot/USD-M，并配置报价资产、基础资产和交易对的黑白名单；输入会转大写、去空格和去重，空白名单不限，所有白名单取交集，任一黑名单命中即排除。多个元数据工作流各自保存成功快照，币种目录显示全部来源并集；停用只停止调度并保留上次成功快照。币种数据页不再提供代理或同步设置，采集状态和实时日志从“查看采集工作流”进入工作流画布查看。

Quant 只连接 Binance Spot 和 USD-M 公共 REST/WebSocket。先创建并激活 `quant-market-data`，选择市场、品种和固定周期；同一 `market + instrument + interval` 的订阅共享连接，断线后 REST 补数，数据库与 CloudEvent 身份共同去重。每根闭合 K 线保存一条 Quant 行情和 CloudEvent，并为命中的工作流创建一条包含完整节点路径的 Run；Run 不复制 OHLCV 正文。月线不属于当前固定周期集合。

K 线连接启动和重连只执行缺口补数与 WebSocket 采集，不触发币种元数据刷新。元数据节点只有在全部选中市场都抓取并解析成功后才替换快照；失败时目录保持不变，过滤后为空则保存为空快照。

`quant-strategy` 消费 `market.candle.closed` 并调用已编译的 SMA crossover Go 策略。`quant-backtest` 读取已落库的闭合 K 线，在下一根 K 线开盘成交并应用 Decimal 手续费和滑点；日期必须是 UTC。运行结果中的 Quant 页面可查看品种、K 线、策略和回测摘要，并下载后校验完整明细 SHA-256。

量化指标判断节点可在一个实例内用“全部/任一”组合最多 4 层、16 个条件，每个条件可独立选择 K 线周期。当前支持放量、N 根 K 线首尾上涨/下跌/绝对涨跌、期间振幅、MACD 金叉/死叉/零轴位置、KDJ 金叉/死叉及 K/D/J 阈值、Wilder RSI 阈值和布林带突破。节点只使用已经闭合且连续的 K 线；历史不足时走 false，不发送通知。

判断节点提供 `true / false` 两个出口，每个出口都可扇出、串联、汇合或连接普通节点。串联判断可表达跨节点 AND，并联汇入同一后继可表达 OR，false 出口可表达反向条件。连续命中仍会执行后续判断；直接把一个或多个判断节点连接到站内通知时，编辑器自动只在整条 true 路径刚刚成立时通知，并聚合本次命中的摘要。路径恢复为 false 后再次成立，会再次发送通知。

### 5.4 Paper 与通知

选择 `quant-paper` 会在一个事务内创建未激活的共享行情工作流和 Paper 策略工作流。先确认市场、品种、周期和五项风险限制，再分别激活两个工作流。默认 `core.human_approval` 和 `paper_execute` 都使用 `human`；自动模式必须同时显式改为 `auto`，且不能删除任一风险上限。

Paper 链路为“闭合 K 线 → 可信策略 → 信号 → 人工/自动决策 → 新鲜公共报价与风险复核 → Paper 账本 → 站内通知”。账户按工作流和 Paper 节点实例隔离。风险拒绝不创建账户、订单或部分账本；成交、费用和账本使用 Decimal 字符串与 UTC 时间，不连接 Binance 私有接口。

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
