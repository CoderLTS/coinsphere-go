# CoinSphere 使用手册

本文面向 CoinSphere 的个人自托管用户和系统管理员，说明从安装到日常使用、备份、升级与排障的完整流程。开发和接口细节分别见[本地开发手册](runbooks/development.md)与[公共契约](contracts/README.md)。

> 当前建议只使用 Paper。Testnet、Spot Live 和 USD-M Live 虽已有后端能力，但默认关闭，当前也没有 Binance 环境晋级证据；本文不提供真实交易放行教程。

## 1. 使用范围

CoinSphere 适合以下场景：

- 在个人服务器或电脑上运行单实例量化平台。
- 用 Paper 验证行情、策略、信号、风控和执行链路。
- 为少量受邀用户提供隔离的账户、策略实例、回测和通知资源。
- 用可视化工作流编排定时任务、事件、智能体和通知。

项目不提供公开注册，也不面向高频交易、多交易所套利、多实例集群或恶意策略代码托管。当前仅支持 PostgreSQL/TimescaleDB。

## 2. 部署方式

| 方式 | 适用场景 | 入口 |
| --- | --- | --- |
| Docker Compose | 首次体验、个人长期运行 | `http://localhost:8080` |
| 本地开发 | 修改 Go、Vue 或 Python 代码 | Web `:3006`、Backend `:6987` |
| 生产部署 | 已维护 Linux 主机和本机镜像仓库 | 独立 CoinSphere Compose，参照发布 Runbook |

首次使用推荐 Docker Compose。生产部署不会自动允许私有交易，详细边界见[发布与回滚](runbooks/release.md)。

## 3. Docker Compose 安装

### 3.1 前置条件

- Docker Engine 或 Docker Desktop，支持 `docker compose` v2。
- 至少能拉取 Docker Hub 镜像；公共行情功能还需要访问 Binance 公共 API。
- 建议预留 4 GB 内存和足够的数据库、上传文件及回测产物空间。
- 生产或长期运行时，应有独立备份位置，不要只保留 Docker Volume。

先检查环境：

```bash
docker version
docker compose version
```

### 3.2 设置签名密钥

签名密钥用于认证令牌，必须使用随机值。不要把实际值提交到仓库、工单或聊天记录。

Linux/macOS：

```bash
export COINSPHERE_AUTH__SECRET_KEY="$(openssl rand -hex 32)"
```

PowerShell：

```powershell
$bytes = New-Object byte[] 32
[Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
$env:COINSPHERE_AUTH__SECRET_KEY = [BitConverter]::ToString($bytes).Replace('-', '').ToLowerInvariant()
```

长期运行时把密钥放入宿主机 Secret 管理或仓库忽略的 `.env`，并限制读取权限。重启时继续使用同一个值；轮换该值会让已有登录令牌全部失效。

### 3.3 启动

在仓库根目录执行：

```bash
docker compose up -d --build
docker compose ps
```

正常情况下：

- `timescaledb` 处于 `healthy`。
- `migrate` 成功退出，退出码为 `0`。
- `backend`、`worker` 和 `web` 正在运行；`executor` 仅属于默认关闭的 `private` profile。
- 浏览器访问 <http://localhost:8080> 能看到登录页。

端口冲突时，启动前设置 `COINSPHERE_WEB_PORT`：

```bash
export COINSPHERE_WEB_PORT=18080
docker compose up -d
```

PowerShell 使用 `$env:COINSPHERE_WEB_PORT = '18080'`。修改后入口为 <http://localhost:18080>。

### 3.4 查看日志和停止

```bash
docker compose logs --tail=200 backend
docker compose logs --tail=200 worker
docker compose logs -f web backend
docker compose down
```

`docker compose down` 只停止并移除容器和网络，命名卷中的数据仍保留。不要执行 `docker compose down -v`，除非明确要永久删除数据库、上传文件和回测产物且已经验证备份。

## 4. 首次登录与安全初始化

### 4.1 登录

Compose 初次启动会创建超级管理员：

```text
用户名：coinsphere
密码：coinsphere
```

默认密码只用于首次种子创建。登录后进入“系统管理 > 用户管理”，编辑 `coinsphere` 用户并设置至少 12 位的唯一密码。编辑已有用户时，“新密码”留空表示保持不变。

没有公开注册入口。需要新增用户时，由管理员在“用户管理”中创建并分配角色。

### 4.2 忘记管理员密码

若仍有另一名管理员，直接在“用户管理”中重置。完全无法登录时，在能安全连接数据库的环境中使用一次性 CLI；密码只从交互式标准输入读取，不要放进命令行参数：

```powershell
cd backend
$env:COINSPHERE_DATABASE__DSN = 'postgresql://<user>:<password>@<host>:5432/<database>?sslmode=require'
go run ./cmd/admin -config ./config.yml -username coinsphere
```

CLI 要求密码至少 12 个字符，并要求输入两次确认。不要把生产 DSN 写入脚本、README、截图或终端共享记录。

### 4.3 首次安全清单

- 修改默认管理员密码，并为日常使用创建非超级管理员账号。
- 保持 Testnet/Live 私有能力开关为 `false`。
- 确认全局急停状态符合预期；首次检查时建议保持急停开启。
- 只绑定本人需要的角色和菜单权限。
- 配置 HTTPS 反向代理后再通过公网访问，不要直接暴露数据库或 Backend 端口。
- 备份签名密钥和数据库，但将密钥与数据库备份分开保管。

## 5. 页面、角色与数据范围

### 5.1 主要页面

| 菜单 | 用途 |
| --- | --- |
| 首页 | 工作流工作台、当前/最近执行和 K 线信号预览 |
| 市场元数据 | 设置 Spot/USD-M 与报价资产范围，手工同步并查看状态 |
| 行情图表 | 查看 K 线、成交量、目标仓位和 BUY/SELL/FLAT 信号 |
| 交易管理 / Paper 账户 | 管理账户、风控、余额、持仓、订单、意图和账本 |
| 工作流定义 | 创建版本、编辑画布、校验、激活和手工执行 |
| 任务定义 | 维护工作流任务节点可用的参数 |
| 执行记录 | 查看状态、节点轨迹、输入输出和错误信息 |
| 新闻数据 / 推送数据 | 管理新闻及查看推送记录 |
| AI 模型 / 智能体配置 | 配置模型连接和智能体模板 |
| 通知渠道 | 配置站内、钉钉、QQ 或 SMTP 通知 |
| 用户 / 角色 / 菜单管理 | 管理账号、RBAC 和前端菜单 |
| 个人中心 | 查看当前用户资料 |

菜单是否可见由角色权限决定。普通用户默认可见首页、Paper 账户、AI 模型、通知渠道和个人中心；超级管理员拥有全部菜单和按钮权限。

### 5.2 数据隔离

- Binance 品种、K 线和管理员发布的策略版本是登录用户共享的只读资源。
- 自选、策略实例、回测、信号、交易账户、订单和通知按所有者隔离。
- 管理员身份不会让普通资源接口自动跨用户返回数据；跨用户管理使用专门的管理员接口。

## 6. Paper 模拟交易

Paper 是当前推荐且正式验收的交易环境。它不会向 Binance 私有接口发送订单，但仍按完整的账户、风控、意图、订单和账本链路运行。

### 6.1 创建账户

1. 进入“交易管理 > Paper 账户”。
2. 点击新增账户按钮。
3. 填写账户名称，环境选择 `Paper`，市场选择 `Spot` 或 `USD-M`。
4. 设置初始余额和 Paper 手续费率。
5. 选择允许交易的品种。
6. 填写全部风险上限并提交。

风险字段包括总名义价值、单品种名义价值、单订单名义价值、单日最大亏损、最大回撤、最大行情年龄；USD-M 还涉及杠杆和保证金相关约束。金额和费率在系统内以十进制字符串处理，时间统一按 UTC 记录。

创建账户不等于启用自动化。新账户、风控变化、对账异常或急停都可能让账户保持暂停。

### 6.2 配置和修改风控

1. 在左侧账户列表选择目标账户。
2. 在风险区检查每项限制，点击编辑。
3. 输入当前登录密码完成复验。
4. 保存后确认页面显示的新限制和账户状态。

所有显式风险上限都应填写。不要通过放大限额来掩盖行情过期、对账差异或仓位归属问题。风控变更可能撤销已有自动化放行，需要重新检查并手工恢复。

### 6.3 恢复暂停账户

只有暂停原因已经处理、风险完整且状态一致时才恢复：

1. 查看账户标题下方的暂停原因和对账摘要。
2. 检查余额、持仓、开放订单及交易意图是否符合预期。
3. 点击恢复账户，输入当前密码复验并确认。
4. 刷新页面，确认账户变为活动状态。

恢复只解除当前暂停，不会自动批准策略或开启自动化。持续对账再次发现差异时会重新暂停。

### 6.4 自动化开关与授权

账户页面提供 Owner 自动化开关和管理员授权。自动执行需要同时满足账户活动、完整风控、无全局急停、状态一致，以及对应的用户与管理员放行。任何一项缺失都应保持禁用。

Paper 可用于验证这条授权链，但首次使用建议保持自动化关闭，先检查人工信号和意图是否符合预期。

### 6.5 查看执行结果

账户详情包含以下视图：

- 余额：权益、可用余额、已实现和未实现盈亏。
- 持仓：品种、数量、开仓价、最新价及 USD-M 风险字段。
- 订单：方向、类型、数量、均价、状态和确定性 `clientOrderId`。
- 交易意图：模式、目标仓位、执行状态和阻断原因。
- 权威账本：成交、手续费、资金费等追加事实。
- 对账摘要：远端/本地状态、风险快照和最近对账结果。

故障排查时先看“交易意图”的阻断原因，再看账户暂停原因和对账摘要；不要仅凭策略信号判断订单已经执行。

### 6.6 全局急停

全局急停会阻止新增风险，只允许系统定义的减仓、平仓或保护动作。触发急停后：

1. 记录触发原因和当前账户状态。
2. 检查全部账户、未完成意图、订单与持仓。
3. 修复根因并完成必要对账。
4. 由管理员输入当前密码复验后解除急停。
5. 逐个恢复账户和授权，不要把解除急停当作批量恢复。

部署、重启、代码合并和对账成功都不应自动解除急停。

## 7. 行情、策略、回测与信号

市场元数据和行情图表已有 Web 页面；策略版本、实例、回测和信号的完整写操作仍以 `/api/v1` 契约为准。

当前 API 能力包括：

- 同步和查询 Binance Spot、USD-M 品种与闭合 K 线。
- 管理用户自选。
- 由管理员创建策略草稿、校验并发布不可变版本。
- 创建策略实例和回测任务，查询运行状态与结果。
- 启动实时计算，查询信号并进行人工批准或拒绝。
- 通过 WebSocket 接收通知和状态变化。

使用 API 时仍需先登录并遵循所有者隔离、RBAC、幂等键和密码复验要求。交易相关写操作不要根据路由名自行猜测请求体；直接采用契约中记录的字段、状态机和错误码。

## 8. 工作流

工作流用于粗粒度业务编排，例如同步行情元数据、订阅或补齐 K 线、计算策略、响应领域事件、调用智能体和发送通知。

### 8.1 创建和激活

1. 需要任务节点时，先在“任务定义”中检查或维护参数。
2. 进入“工作流定义”，新建工作流。
3. 在画布放置开始、行情、策略、任务/智能体、控制、通知等节点和结束节点。
4. 配置每个节点及连线，使用工具栏校验。
5. 校验通过后保存为定义版本。
6. 在版本列表中激活目标版本。
7. 对手工入口执行一次测试，并在“执行记录”查看完整节点轨迹。

编辑已使用的流程时创建新版本；激活新版本前保留可回退的旧版本。当前激活版本和已有执行记录的版本会受到删除保护。

### 8.2 运行边界

- 工作流不得调用 Binance 私有接口、保存交易凭据、创建交易命令或绕过风控。
- 通用 HTTP 节点只访问经过允许的公网服务，不把令牌或原始敏感载荷写入日志。
- 策略节点只负责创建并等待 Worker 任务；逐 K 线计算循环仍由行情模块和 Worker 执行。
- 事件触发每次会产生独立执行记录；失败时从执行记录查看具体节点错误。

## 9. 通知渠道

系统支持站内、钉钉、QQ 和 SMTP 渠道。站内渠道由系统内置；外部渠道需要用户提供对应平台配置。

配置流程：

1. 进入“配置管理 > 通知渠道”。
2. 新增渠道并选择类型。
3. 填写该类型要求的地址、凭据或收件配置。
4. 保存后执行测试，确认目标端收到消息。
5. 测试成功后再启用，并在工作流或智能体中引用。

渠道配置按用户隔离。令牌、Webhook Secret、SMTP 密码等只填入受控配置表单，不要写入工作流文本、代码、日志或截图。停用渠道前检查仍在使用它的工作流。

## 10. AI 模型与智能体

### 10.1 配置模型

1. 进入“配置管理 > AI 模型”。
2. 新增模型，选择供应商类型或预设。
3. 填写供应商名称、模型标识、Base URL 和凭据。
4. 保存并启用模型。
5. 需要时将模型绑定为智能体使用的模型。

Base URL 是外部信任边界，只配置可信的 HTTPS 服务。模型凭据不得进入提示词、工作流状态、日志或版本库。

### 10.2 配置智能体

1. 进入“智能体配置”，新增或编辑模板。
2. 设置名称、系统提示词、头像和数据源约束。
3. 绑定已启用模型并保存。
4. 在工作流智能体节点中选择该智能体，配置输入与输出路径。
5. 用非敏感测试数据验证结果，再用于正式工作流。

智能体只能生成分析或编排结果，不能直接获得交易所私有访问权，也不能绕过信号审批、账户风控或 Executor。

## 11. 用户、角色与菜单

### 11.1 用户

管理员在“用户管理”中创建、编辑、启停用户并分配角色。停用用户前确认其工作流、通知和量化任务的归属及后续处理方式。

### 11.2 角色

角色同时控制菜单和按钮级权限。采用最小权限原则：日常账号只授予实际使用的页面与动作，超级管理员只用于系统维护。

### 11.3 菜单

菜单管理决定前端导航结构，但隐藏菜单不等于 API 授权。真正的访问控制仍由后端 RBAC 执行；不要把菜单可见性当作安全边界。

## 12. 数据、备份与恢复

Compose 默认持久化：

| Volume | 内容 |
| --- | --- |
| `timescale-data` | PostgreSQL/TimescaleDB 数据 |
| `backend-uploads` | 用户上传文件 |
| `backend-static` | 后端静态文件 |
| `worker-artifacts` | 回测冻结产物 |

实际 Volume 名可能带 Compose 项目前缀，可用 `docker volume ls` 和 `docker compose config --volumes` 确认。

数据库应使用 PostgreSQL 原生工具执行一致性备份，并同时备份上传文件和 Worker 产物。一个可恢复的备份集合至少包含：

- 数据库备份及其 PostgreSQL/TimescaleDB 版本信息。
- `backend-uploads` 和 `worker-artifacts` 中对应文件。
- 不与数据放在一起的运行配置和密钥备份。
- 备份时间、应用 Commit 或镜像 digest、migration 版本。

恢复必须先在隔离环境演练：恢复数据库和文件，使用目标版本运行 migration 校验，再检查 `/health/ready`、登录、工作流记录、Paper 账户和回测产物。不要在未验证备份时删除旧 Volume。数据库 migration 的 Up/Down 规则见[迁移手册](runbooks/database-migrations.md)。

## 13. 升级与部署

### 13.1 个人 Compose 升级

1. 记录当前 Commit，备份数据库、上传文件和回测产物。
2. 阅读目标版本的 migration 和变更说明。
3. 拉取已审查的代码。
4. 使用原有签名密钥执行 `docker compose up -d --build`。
5. 确认 `migrate` 成功、所有长期服务正常、健康检查通过。
6. 登录并检查首页、工作流和 Paper 账户状态。

不要用 `down -v` 作为升级步骤。migration 失败时停止候选服务，保留数据库和文件，按 Runbook 判断前滚修复或回滚应用；不要自行删除 schema 或交易事件。

### 13.2 生产部署

生产 Deploy 是手工触发的“构建镜像 -> 扫描镜像 -> 固定 digest 部署”流程，不要求每次创建 GitHub Release。只有需要发布版本包、Release 页面和版本标签时才运行 Release 流程。

具体触发条件、Runner、Manifest、健康检查和回滚命令统一维护在[发布与回滚](runbooks/release.md)，本文不复制生产路径和服务器配置。

## 14. 健康检查与排障

### 14.1 健康端点

通过 Web 入口检查：

```bash
curl -fsS http://localhost:8080/health/live
curl -fsS http://localhost:8080/health/ready
curl -fsS http://localhost:8080/metrics
```

- `/health/live`：进程可以响应。
- `/health/ready`：Backend 能在预算内连接 PostgreSQL。
- `/health`：兼容的就绪检查。
- `/metrics`：固定、无标签的进程指标。

检查 Worker：

```bash
docker compose exec -T worker python -m coinsphere_worker health
```

### 14.2 常见问题

| 现象 | 优先检查 |
| --- | --- |
| Compose 提示缺少签名密钥 | 当前 shell 是否设置 `COINSPHERE_AUTH__SECRET_KEY` |
| `migrate` 失败 | `timescaledb` 健康、迁移日志、数据库是否被旧版本占用 |
| Web 打不开 | `web` 端口映射、端口冲突、`backend` 是否 healthy |
| 登录后立即失效 | 重启时是否更换了签名密钥、浏览器时间是否异常 |
| 行情没有更新 | Binance 公共网络、Backend 日志、自选或策略订阅范围 |
| 回测一直等待 | `worker` 健康、backtest lane 任务租约、数据库和产物目录空间 |
| 实时信号不生成 | `worker` 健康、闭合 K 线、策略实例状态和输入数据 |
| Paper 意图被阻断 | 全局急停、账户暂停、风控、行情年龄、授权和阻断原因 |
| 通知失败 | 渠道启用状态、测试结果、外部网络和供应商配置 |

诊断时使用：

```bash
docker compose ps
docker compose logs --tail=200 migrate backend worker web
docker system df
```

日志和错误截图必须先移除 DSN、令牌、凭据、原始外部载荷和个人数据。

## 15. 默认关闭的私有交易能力

以下开关默认均为 `false`：

```text
COINSPHERE_TRADING__TESTNET_PRIVATE_API_ENABLED
COINSPHERE_TRADING__SPOT_LIVE_MANUAL_ENABLED
COINSPHERE_TRADING__SPOT_LIVE_AUTO_ENABLED
COINSPHERE_TRADING__USD_M_LIVE_MANUAL_ENABLED
COINSPHERE_TRADING__USD_M_LIVE_AUTO_ENABLED
```

私有能力还要求独立安全的 `COINSPHERE_AUTH__ENCRYPTION_KEY`。Live auto 必须同时启用对应 manual，并满足管理员授权、Owner 放行、完整风控和持续匹配对账；USD-M 还要求逐仓、单向、受限杠杆和强平距离证据。

这些是必要条件，不是当前启用建议。CI、Codex、自动部署和工作流不得提供真实凭据、打开上述开关、发起私有请求或解除急停。晋级顺序和证据要求以[开发计划](roadmap/README.md)、[架构说明](architecture/overview.md)和[发布 Runbook](runbooks/release.md)为准。

## 16. 文档索引

- [README](../README.md)：项目定位和快速启动
- [架构说明](architecture/overview.md)：组件职责、数据归属和关键数据流
- [公共契约](contracts/README.md)：API、幂等、复验和交易状态语义
- [开发计划](roadmap/README.md)：能力顺序、完成标准和晋级门禁
- [质量门禁](quality/quality-gates.md)：本地与 CI 验收标准
- [本地开发](runbooks/development.md)：开发命令和运行时诊断
- [数据库迁移](runbooks/database-migrations.md)：迁移、校验和回滚
- [发布与回滚](runbooks/release.md)：构建、扫描、固定 digest 部署和回滚
