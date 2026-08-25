# CoinSphere 公共契约

本文只冻结当前已实现的跨模块语义。目标能力以[工作流平台 V2](../architecture/workflow-platform-v2.md)和[路线图](../roadmap/README.md)为准；未实现路由不得视为可用接口。

## 通用边界

- 领域时间统一使用 UTC。数据库使用 `TIMESTAMPTZ`，HTTP 和事件使用带时区的 RFC 3339。
- 金融价格、数量、金额、费率和盈亏使用 Decimal；数据库使用 `NUMERIC(38,18)`，JSON 使用十进制字符串。
- 外部枚举、时间、ID、Decimal、URL、文件和凭据在信任边界校验；模块不变量由拥有模块或数据库约束保证。
- 错误响应使用 `application/problem+json`，包含 `type`、`title`、`status`、`detail` 和 `requestId`。

## 身份与 HTTP

- 不提供公开注册。`POST /api/v1/auth/login` 是唯一匿名身份 API。
- `POST /api/v1/auth/logout`、`POST /api/v1/auth/reauth` 和 `GET /api/v1/me` 要求有效 Access Token。
- `POST /api/v1/auth/reauth` 返回绑定当前用户与当前会话、五分钟失效且只能使用一次的不透明 Token。
- 当前业务 API 包含 `/api/v1/home/*`、`/api/v1/admin/users`、`/api/v1/system/*` 和工作流 P1-A/P1-B 路由；工作流路由只允许 `R_SUPER`。
- `/health/live` 只报告进程存活；`/health/ready` 和 `/health` 在一秒预算内检查 PostgreSQL；`/metrics` 要求登录。
- 旧行情、策略、回测、信号、通知和交易路由已移除，不提供别名或兼容响应。

## 工作流 P1-A/P1-B

| 路由                                                        | 语义                                   |
| ----------------------------------------------------------- | -------------------------------------- |
| `GET /api/v1/workflows/templates`                           | 列出当前可创建的模板；目前只有 `blank` |
| `GET /api/v1/workflows/node-definitions`                    | 列出核心与编译期插件节点 Schema        |
| `GET/POST /api/v1/workflows`                                | 列表，或从模板创建工作流及初始修订     |
| `GET /api/v1/workflows/{workflowId}`                        | 读取元数据、活动修订和唯一运行实例摘要 |
| `GET/POST /api/v1/workflows/{workflowId}/revisions`         | 列表，或保存新不可变修订               |
| `GET /api/v1/workflows/{workflowId}/revisions/{revisionId}` | 读取固定修订                           |
| `POST /api/v1/workflows/{workflowId}/lifecycle`             | 执行 `start`、`pause` 或 `archive`     |

- 创建只接受 `batch` blank 模板：`core.manual` 的 `out` 端口连接 `core.end` 的 `in` 端口。图 `schemaVersion` 固定为 `1`，节点保存 `nodeInstanceId`、精确节点版本、普通配置、结构化输入映射和位置；边保存两端端口及可选 Boolean CEL 条件。
- 输入映射只接受 `field`、`literal`、`cel`。字段来源使用上游 `nodeInstanceId` 和字段路径数组；保存校验端口、可达性、DAG、JSON Schema、字段类型和 CEL，并拒绝 Decimal CEL 算术。
- 保存请求必须提供当前 `expectedActiveRevisionId`。服务锁定工作流，校验完整图，写入递增修订、修订级密钥绑定并原子切换活动指针；并发旧指针返回 `409 Conflict`，失败校验不创建修订。
- `secretChanges` 只允许替换或移除节点 Config Schema 声明的顶层 `x-coinsphere-secret` 字段。密钥按修订、节点实例和字段独立加密；响应只返回 `secretFields[nodeInstanceId][field]=true`，图、修订响应和节点目录永不返回密钥值。
- 修订保存后不可更新或删除。归档工作流只读且不能重新启动；`archive` 只允许从 `paused` 或 `needs_attention` 执行。
- `start` 当前只改变生命周期状态，不创建执行批次或调用插件 Action；这些能力由 P1-C 交付。

## 插件清单

每个可信本地插件根目录包含唯一的 `coinsphere-plugin.json`：

- `schemaVersion` 当前固定为 `1`。
- `id` 是稳定的小写点分名称；`version` 是严格 SemVer。
- `sdkMajor` 必须等于当前 SDK major，`requiresCore` 必须包含当前 Core 版本。
- Backend 入口必须是拥有匹配 module 名的 Go module；Frontend 和 migration 路径必须留在插件根目录内。
- `contributes` 只接受 `nodes`、`triggers`、`apiRoutes`、`resultPages` 和 `migrations`，声明的非 migration 贡献必须实际注册。

`plugin validate` 只读校验一个或多个目录。应用启动只执行生成的 Go 注册表，不扫描插件目录或动态加载共享库。

## SDK

Action 描述符固定节点类型、SemVer、Config/UI/Input/Output Schema、执行池、副作用等级和状态模式。需要校验的 Schema 必须声明 JSON Schema 2020-12。

`ActionRequest` 包含固定工作流/修订、节点实例 ID、稳定操作键、已解析输入和配置，以及 SecretReader、StateStore、ArtifactStore 和结构化 Logger。`ActionResult` 只返回 JSON 输出和制品引用。P0 注册并可在契约测试上下文执行 Action；工作流运行时从 P1 开始接入。

插件作用域路由必须声明一种上下文：

- `WorkflowScope`：当前工作流和节点范围，仅供工作流管理面。
- `ResultScope`：固定视图、插件、页面、服务端过滤器、操作白名单和当前用户。
- `SystemScope`：插件安装状态和系统级健康范围。

插件不得从查询参数扩大核心注入的范围。P0 契约测试证明 `ResultScope` 固定过滤器覆盖不可信查询参数；生产共享视图和授权存储在 P4 实现。

结果页描述符包含插件内唯一 `pageKey`、标题、前端组件入口、范围 Schema、操作白名单和移动端能力。Vue 生成注册表把每个插件入口约束为 `FrontendPluginModule`；测试契约插件不加入生产菜单。

## 生命周期与数据

- `plugin install` 校验源码、执行插件 migration、复制源码、生成 Go/Vue 注册表、更新 Go module 并构建 Backend/Web 镜像。
- `plugin upgrade` 只接受版本递增的同 major 升级；既有 migration 文件必须字节不变，新 migration 版本只能追加。
- 安装或升级失败恢复构建输入和操作前插件 migration 版本；构建命令不启动或切换运行容器。
- `plugin uninstall` 有活动引用时拒绝；成功后移除静态源码和注册并保留插件 schema。
- `plugin purge-data` 要求插件已卸载、无任何活动或历史引用，并精确提供 `PURGE <plugin-id>`；schema 和安装记录在同一事务删除。

核心 migration 使用 `schema_migrations`。插件使用独立 `plugin_<规范化 ID>` schema 和 `<schema>.schema_migrations`；卸载不执行 Down。正式 Paper 观察开始前 migration 历史尚未冻结，但任何破坏性整理都必须重跑空库和保护测试。

## 尚未实现

批次执行、检查点、历史制品、事件流、Connector/AI、Quant、回测、信号、Paper、Notification 和共享结果视图当前均不可用。Testnet/Live、私有交易 API、插件市场、签名、沙箱、热加载和多实例集群不属于 V2 当前合同。
