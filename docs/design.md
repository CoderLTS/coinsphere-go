# CoinSphere 设计与运行手册

本文是 CoinSphere 的唯一设计事实来源，覆盖架构、插件、权限、页面、Graph v3、迁移、质量门禁和运行边界。根目录 README 只保留入口和快速启动信息。

## 1. 系统边界

CoinSphere 是以可视化工作流为核心、由可信编译期插件扩展的模块化单体。一个 Go 进程提供 API、工作流执行器、WebSocket 和 Vue 静态资源，PostgreSQL 16 保存业务状态、队列、幂等约束和审计数据。系统不依赖 Redis、消息中间件或 Kubernetes，当前会话撤销和重新认证令牌保存在进程内存，因此部署边界是单实例。

领域时间使用 UTC，价格、数量、金额和费率使用 Decimal，JSON 中的金融值使用十进制字符串。工作流、AI 和通用 HTTP 节点不得调用交易所私有接口或绕过风控；真实交易默认关闭，任何放行都需要独立风控、观察证据和人工授权。

## 2. 代码与状态所有权

后端保持模块化单体，但按状态所有权组织代码：认证与会话、RBAC 与菜单、工作流定义和修订、Graph 校验、运行队列与 Lease、节点执行与 Checkpoint、Trigger/Event/Outbox、等待与人工任务、制品与运行历史、ResultView、插件生命周期和系统监控。模型按同样的领域边界拆分，禁止把队列、节点执行、凭据读取和 API 编排重新堆回一个服务文件。

PostgreSQL 同时承担业务事实、持久队列、恢复点、Outbox 和幂等约束。状态拥有模块负责写结构化日志，日志不得包含密钥、令牌、原始载荷或个人数据。未知错误只向客户端返回通用 Problem Details，原始错误写入脱敏日志。

## 3. 插件契约

插件是可信源码，在构建期注册到 Go 和 Vue 的静态注册表。插件可以提供节点、Trigger、Profile、ResultPage、系统页、ResultView 处理器、Assistant Query、路由和独立 migration。插件版本由 manifest、SDK 和生成注册表共同校验；安装、升级、卸载及数据清理由生命周期管理器执行。

依赖解析遇到缺失插件或循环依赖必须让启动失败，不能静默跳过。工作流节点定义返回 `pluginId`，前端通过显式插件索引加载编辑器、渲染器和配置组件，不再扫描全部插件模块。

Profile 是唯一的连接和凭据拥有者。通用 `connections` 表、API 和编辑器已删除；工作流只保存经过插件校验的 Profile 引用。密钥不进入导出文件、日志、运行历史或前端代码。

Core4 仍为 AI、通知、QQ 和连接器的旧固定字段节点保留内部加密 Profile 快照桥接层；它不再作为公开连接模型或前端编辑入口。新插件必须使用 SDK 的 `ProfileProvider` 和插件自有 Profile 表，旧桥接数据在迁移时只导出非敏感字段，导入后须重新录入凭据。

## 4. 权限与页面

`R_SUPER` 负责用户、角色、菜单、插件、工作流定义、Profile、运行控制和 ResultView 授权管理。普通用户不管理工作流，只能访问被授予的 ResultView，且每个动作同时受 ResultView `AllowedActions`、插件声明动作和业务状态校验约束。系统插件路由继续使用 `R_SUPER`。

前端只使用后端会话、菜单和 `/me.permissions`，不再存在 guest access mode 或前端权限模式。工作流管理页面在正式菜单中只对 `R_SUPER` 显示；ResultView 使用正式 `/results` 路由，页面由目录和 Schema 驱动并保持现有视觉体系。页面文案统一使用 i18n。

登录令牌按当前部署约定保存在 `localStorage`；这是会话便利性选择，不是安全边界，必须配合明确有效期、退出清理和撤销语义。客户端锁屏只保留会话内存状态，不持久化“加密”密码，也不替代后端鉴权。

菜单只允许内部页面和插件页面。外部 URL、iframe 菜单字段、静态 iframe 路由和内嵌管理器全部删除，CSP 不再放行第三方页面或图标 CDN。图标在构建期打包。

## 5. Graph v3

运行时只接受 `schemaVersion: 3`。节点输入绑定使用 `literal`、`node`、`event` 和 `profile`，入口和条件由显式节点表达，运行快照包含可恢复执行所需的配置和版本信息。未知节点、未知字段、复杂 CEL 或不确定的旧语义必须阻止导入并生成逐工作流报告，禁止隐式猜测。

## 6. 一次性迁移

迁移命令：

```text
go run ./cmd/coinsphere workflow export-legacy --config ... --out ...
go run ./cmd/coinsphere workflow import-v3 --config ... --in ... --report ...
```

导出阶段使用旧数据库的原始 SQL 读取工作流定义、分组、Graph、运行配置及非敏感 Profile 配置，不读取运行历史、日志、制品或密钥。转换规则是单 Trigger 转入口，旧字段/CEL 绑定转为 Graph v3 binding，可机械表达的边条件转为显式条件节点；不可转换项进入报告并跳过。

切换顺序固定为：导出并归档文件，重建 PostgreSQL 16 和单一 Core4 基线，执行全量校验导入，重新录入密钥，验证工作流、Trigger、Profile、ResultView 和权限。导入使用单事务，任何错误都回滚；业务 ID 可重新生成。旧运行历史、日志、制品和密钥不保留。

## 7. 质量门禁

CI 必须在 PostgreSQL 16 环境执行 Go 测试、gofmt、vet、staticcheck、govulncheck 和构建，并执行前端 lint、stylelint、构建以及当前 E2E。测试覆盖 Graph 转换和原子导入、插件依赖错误、ResultView 自定义动作、权限 fail-closed、错误脱敏、UTC、停机释放和精确路由匹配。

验收必须确认：不存在通用 connections API/表、guest/iframe/旧路由和孤立文档引用；插件依赖错误能阻止启动；ResultView 可由普通用户正式访问；迁移报告完整；重新录入凭据后导入的 Graph v3 工作流可运行。

## 8. 运维限制

真实交易密钥不得进入代码、日志、Issue、PR、CI 或 AI 上下文。发布、部署、合并和真实交易均不由本任务自动执行。任何多实例部署、持久化会话、破坏性交易能力或跨域外部页面需求都必须新增架构决策并重新建立安全边界。
