# CoinSphere 使用手册

本文面向当前 V2 个人自托管用户和管理员。当前可用能力是登录、RBAC、系统管理、系统监控、可信本地插件维护，以及工作流、Connector/AI、Binance 公共行情、可信 Go 策略、回测、Paper 和站内通知。

## 1. 使用范围

- 只支持 PostgreSQL/TimescaleDB。
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

Compose 依次启动 `timescaledb`、一次性 `migrate`、`backend` 和 `web`。浏览器打开 <http://localhost:8080>。

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
| 工作流定义 | 使用原画布布局创建、编辑、启停和手工运行 V2 工作流         |
| 执行记录   | 查看工作流批次、节点运行状态和受控错误摘要                 |
| 币种数据   | 使用 `official.quant` 独立页面，以原形式查看交易标的元数据 |
| K 线详情   | 使用 `official.quant` 独立页面，以原形式查看闭合 K 线      |
| 系统管理   | 维护用户、角色、菜单与权限码                               |

导航和页面布局沿用原 `/scheduler/*`、`/data/*` 形态，底层工作流、修订、批次和插件接口仍使用 V2 契约。旧交易、新闻和模型配置接口未恢复，因此对应菜单不展示；系统也不提供“共享结果”聚合菜单。

## 5. 工作流定义

只有超级管理员可以访问工作流定义页面和 `/api/v1/workflows`。页面保留原工作流列表、版本窗口、运行态抽屉和 X6 编辑器；节点来源、不可变修订、生命周期和批次执行使用 V2 服务。编辑器按核心与编译期插件下发的 JSON Schema/UI Schema 展示节点配置，保存后生成并激活新修订，手工工作流可从列表立即运行。

保存完整校验通过后才创建新修订。密钥输入只表示替换，留空保持原值；系统只显示是否已配置。同一节点实例的类型和版本不变时保留状态。切换到历史修订时工作台只读。

启动后，手工工作流可用“立即运行”创建固定当前修订的批次；定时工作流按 `everySeconds` 在 UTC 时间线上去重入队。事件按 `partitionkey` 保证同工作流同分区顺序，不同分区可并行。失败节点默认重试三次并从最后成功检查点继续；活动栏可取消、重试失败批次或创建固定事件/修订的诊断重放。暂停会取消连续流 Trigger，并在当前 Action 完成后停止领取新批次。连续流异常进入“需处理”后，确认修复可先标记回暂停再重新启动。归档后工作流只读且不能重新启动。

桌面工作台底部“运行活动”通过 WebSocket 接收增量并按游标补齐断线期间记录。选择带批次编号的活动可查看节点尝试、Loop 轮次、执行池、耗时、错误类别和制品；Connector/AI 节点可打开插件结果页。下载制品前后分别由服务端和浏览器校验 SHA-256。活动只显示受控摘要，不包含原始载荷、输出或密钥。终态执行历史默认保留 30 天。

`core.human_approval` 进入等待后释放执行与分区容量。右上角待办徽标打开人工任务列表；批准、拒绝、过期或被相同业务键的新任务取代后，原批次从持久检查点继续。每个任务只能决定一次。

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

Quant 只连接 Binance Spot 和 USD-M 公共 REST/WebSocket。先创建并启动 `quant-market-data`，选择市场、品种和固定周期；同一 `market + instrument + interval` 的订阅共享连接，断线后 REST 补数，数据库与 CloudEvent 身份共同去重。月线不属于当前固定周期集合。

`quant-strategy` 消费 `market.candle.closed` 并调用已编译的 SMA crossover Go 策略。`quant-backtest` 读取已落库的闭合 K 线，在下一根 K 线开盘成交并应用 Decimal 手续费和滑点；日期必须是 UTC。运行结果中的 Quant 页面可查看品种、K 线、策略和回测摘要，并下载后校验完整明细 SHA-256。

### 5.4 Paper 与通知

选择 `quant-paper` 会在一个事务内创建暂停状态的共享行情工作流和 Paper 策略工作流。先确认市场、品种、周期和五项风险限制，再分别启动两个工作流。默认 `core.human_approval` 和 `paper_execute` 都使用 `human`；自动模式必须同时显式改为 `auto`，且不能删除任一风险上限。

Paper 链路为“闭合 K 线 → 可信策略 → 信号 → 人工/自动决策 → 新鲜公共报价与风险复核 → Paper 账本 → 站内通知”。账户按工作流和 Paper 节点实例隔离。风险拒绝不创建账户、订单或部分账本；成交、费用和账本使用 Decimal 字符串与 UTC 时间，不连接 Binance 私有接口。

Paper 页面展示账户、持仓、最近脱敏批次和信号；移动端使用纵向信号队列。管理员可从系统 Quant 路由执行账户投影重建。具体恢复步骤与证据项见 [Paper 恢复与观察](runbooks/paper-recovery.md)。当前仍不提供在线策略源码、Testnet、Live、交易所私有凭据或真实下单。

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

## 7. 数据、备份与升级

持久数据位于 TimescaleDB、上传目录和 Backend 制品卷。升级前至少保存：

- PostgreSQL 一致性备份及恢复命令。
- 上传目录备份。
- `backend-artifacts` 制品卷备份；它必须和数据库快照属于同一恢复点。
- 当前应用版本、Compose 配置和镜像 digest。
- `COINSPHERE_AUTH__SECRET_KEY` 的安全离线副本。

升级使用最新固定版本镜像先运行 migration，再启动 Backend/Web。应用启动只校验 schema，不自动建表。失败时停止候选版本并按[发布 Runbook](runbooks/release.md)恢复匹配的应用镜像；不要手工改 migration 账本或自动执行 Down。

P4 发布标签记录 migration freeze 提交；从该提交开始，已有 migration 只能保持字节不变并追加新版本。任何数据重置都必须按[数据库迁移 Runbook](runbooks/database-migrations.md)确认目标、备份和是否已有 Paper 观察证据。

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
| `migrate` 失败           | TimescaleDB 健康、核心版本、目标数据库是否包含旧 schema |
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
- 完成或部署 P4 不会自动创建 Testnet/Live 阶段，也不会启用任何真实交易。

## 10. 文档索引

- [架构概览](architecture/overview.md)
- [公共契约](contracts/README.md)
- [V2 路线图](roadmap/README.md)
- [质量门禁](quality/quality-gates.md)
- [本地开发](runbooks/development.md)
- [数据库迁移](runbooks/database-migrations.md)
- [Paper 恢复与观察](runbooks/paper-recovery.md)
- [发布与回滚](runbooks/release.md)
