# CoinSphere 代码结构

本文按当前仓库说明代码入口、模块职责和常见修改位置。系统设计见[当前架构](architecture/overview.md)，跨模块语义见[公共契约](contracts/README.md)。目录中存在源码不等于能力已对外开放；实际运行面同时受 Backend 路由、种子菜单、权限和插件注册表约束。

平台助手的主要修改入口如下：

- `backend/internal/service/ai_models.go`：全局模型配置、加密密钥和受限 HTTP 客户端。
- `backend/internal/service/assistant*.go`：会话、SSE、模型工具循环、核心查询及工作流提案确认。
- `backend/internal/api/handlers_assistant.go`：模型和助手 HTTP 接口。
- `backend/plugin/sdk/{types,registry}.go`：插件 `assistantQueries` 注册、Schema 校验和结果上限。
- `frontend/src/components/core/layouts/art-chat-window/`：平台助手抽屉与富文本展示。
- `frontend/src/views/config/ai-model/`：系统管理下的模型配置页面。

## 1. 仓库根目录

```text
coinsphere-go/
├─ .github/             CI、Security、Release 和 Deploy 工作流
├─ backend/             Go App、CLI、migration 和插件 SDK
├─ deploy/              生产 Compose 与二进制发布包说明
├─ docs/                架构、契约、开发指南、质量门禁和 Runbook
├─ frontend/            Vue 3 + Vite Web
├─ scripts/             本地验证及发布、部署辅助脚本
├─ AGENTS.md            仓库开发硬约束
├─ README.md            项目入口与快速启动
├─ docker-compose.yml   本地 PostgreSQL、migration、单应用容器
└─ renovate.json        依赖更新策略
```

当前受版本控制的运行系统没有 Python Worker 或仓库根 `plugins/`。外部插件安装后进入 Backend/Frontend 各自的 `installed` 目录并由生成注册表引用，这些目录不是手工维护的官方插件源码位置。

## 2. Backend

Backend 是 Go module `coinsphere/backend`，入口和公共扩展边界集中在少量目录中：

```text
backend/
├─ main.go                         HTTP 服务与工作流执行器入口
├─ cmd/
│  ├─ admin/                       超级管理员密码恢复
│  ├─ coinsphere/                  插件 validate/install/upgrade/uninstall/purge-data
│  └─ migrate/                     独立核心 migration CLI
├─ internal/
│  ├─ api/                         HTTP、WebSocket、中间件和响应编码
│  ├─ config/                      YAML 配置、环境变量覆盖与校验
│  ├─ db/                          PostgreSQL 连接、模型和幂等种子
│  ├─ migration/                   核心/插件 Goose runner 与 SQL
│  ├─ perm/                        权限码和菜单权限映射
│  ├─ pluginbuild/                 Go/Vue 静态注册表生成
│  ├─ pluginlifecycle/             插件安装事务与失败恢复
│  ├─ pluginregistry/              已安装插件的生成 Go 注册表
│  ├─ security/                    密码、Token、密钥和随机值
│  └─ service/                     业务状态、事务和工作流运行时
├─ plugin/
│  ├─ contracttest/                第三方插件契约测试助手
│  ├─ manifest/                    coinsphere-plugin.json 校验
│  ├─ official/                    Connector、AI、Quant、Notification、QQ
│  └─ sdk/                         插件公共 Go 接口与 Registry
├─ version/                        Core 与 SDK 兼容版本
├─ config.yml                      默认配置
├─ Dockerfile
└─ go.mod
```

### 2.1 进程入口

`main.go` 负责组装，不承载领域逻辑：加载配置、连接数据库、校验 migration、注册插件、执行种子、启动系统日志和工作流执行器、创建 HTTP Server，并在信号到来时有界关闭。

`cmd/migrate` 是唯一核心 DDL 入口；应用启动只读校验数据库版本。`cmd/coinsphere` 是插件生命周期入口，`validate` 不写仓库或数据库，其余动作需要目标数据库和 Backend/Frontend checkout。

### 2.2 API 层

`internal/api/routes.go` 是公开 HTTP 路由总入口，插件 Result/System 路由也在这里挂载。`handlers_*.go` 按工作流、ResultView、系统和系统日志拆分边界解析；`workflow_websocket.go` 实现 `coinsphere.workflow-runs.v1` 运行更新通道；`api.go` 负责认证、权限、Problem Details 和通用响应。

API 层可以校验 ID、枚举、时间、分页、文件和请求体等外部输入，但业务状态转换与事务必须留在 `internal/service`。

### 2.3 Service 层

`internal/service` 按状态所有权拆分：

| 文件组 | 职责 |
| --- | --- |
| `auth.go` | 登录、登出、重新认证、Token 撤销和 Principal |
| `system.go`、`home.go`、`observability.go`、`system_logs.go` | 用户/角色/菜单、首页健康、指标和结构化系统日志 |
| `workflow.go`、`workflow_graph.go`、`workflow_nodes.go`、`workflow_secrets.go` | 工作流定义、图校验、节点目录、不可变修订和密钥 |
| `workflow_run.go`、`workflow_history.go`、`workflow_logs.go` | Run 队列、执行、重试/取消/重放、检查点、制品和历史 |
| `workflow_events.go`、`workflow_triggers.go`、`workflow_schedule.go` | CloudEvents、投递、Outbox、连续流和定时触发 |
| `workflow_human_tasks.go`、`workflow_builtin.go` | 人工等待和核心节点 |
| `workflow_realtime.go` | 进程内运行更新订阅；数据库仍是事实源 |
| `result_views.go` | 固定插件结果范围、授权和白名单操作 |

`service.App` 持有数据库、配置、安全组件、插件 Registry、制品根目录和有界运行资源。跨文件调用仍属于同一模块，不额外引入接口层。

### 2.4 数据与 migration

`internal/db/models.go` 保存核心 GORM 模型，`seed.go` 幂等写入基础角色、菜单、初始管理员和插件页面。版本化 SQL 位于 `internal/migration/sql/`：

| 版本 | 当前职责 |
| --- | --- |
| `00001` | 认证、RBAC、菜单、i18n 和审计 |
| `00002` | 插件安装与引用 |
| `00003` | 工作流、修订和运行时 |
| `00004` | 修订级节点密钥 |
| `00005` | 事件、Run、节点、日志、检查点、状态、制品、Outbox 和人工任务 |
| `00006` | Quant 品种、K 线和回测 |
| `00007` | ResultView、信号、Paper 和 Notification |
| `00008` | 工作流级 Quant 品种来源 |
| `00009` | 结构化系统日志与运行配置 |

不要在模型初始化或应用启动中加入自动建表。数据库变更应修改新 migration、拥有该状态的 Service 和对应契约文档。

### 2.5 插件代码

`plugin/sdk` 和 `plugin/manifest` 是外部插件作者可依赖的公共边界；`internal/*` 不属于插件 API。`plugin/official` 使用同一 Registry 注册内置插件，但其 migration 随核心版本发布。外部插件的安装代码、生成文件和依赖替换由 `internal/pluginlifecycle` 与 `internal/pluginbuild` 管理。

`plugin/official` 的根包只负责编排注册，各插件实现按目录隔离：

```text
official/
├─ register.go
├─ ai/
├─ connector/
├─ notification/
├─ qq/
├─ quant/
└─ internal/safehttp/    官方插件共享的公网访问与 SSRF 防护
```

以下文件由工具生成，不应手工编辑：

- `backend/internal/pluginregistry/registry.generated.go`
- `frontend/src/plugins/registry.generated.ts`

## 3. Frontend

Frontend 使用 Vue 3、TypeScript、Vite、Pinia 和 Element Plus。

```text
frontend/src/
├─ main.ts、App.vue        应用入口
├─ api/                    `/api/v1` 请求、WebSocket URL 与 DTO
├─ assets/                 样式、图片和字体资源
├─ components/             通用布局、表格、表单和业务组件
├─ config/                 UI 与运行配置
├─ directives/             权限和交互指令
├─ hooks/                  可复用组合逻辑
├─ locales/                中英文文案
├─ plugins/                前端插件 SDK、官方插件和生成注册表
├─ router/                 静态/动态路由、菜单转换和守卫
├─ store/                  Pinia 状态，包括用户、菜单、通知和工作流编辑器
├─ types/                  API、路由、组件和 Store 类型
├─ utils/                  HTTP、日期、存储、安全和 UI 工具
└─ views/                  页面实现
```

### 3.1 启动与导航

`main.ts` 初始化应用、Store、Router、国际化和 UI 插件。登录后，Backend 返回的菜单与前端组件别名共同决定可访问导航；仅存在于 `views/` 或静态 route module 的旧页面不能据此视为已支持的公开能力。

`router/core/ComponentLoader.ts` 解析常规页面和插件页面。权限既由前端路由与按钮指令控制，也必须在 Backend 再次校验；前端隐藏不构成授权。

### 3.2 API 与状态

`api/workflows.ts`、`resultViews.ts`、`system.ts` 等文件封装当前 Backend 路由。HTTP 错误由 `utils/http` 统一映射；页面不自行拼接权限或领域状态。`store/modules/workflow-editor.ts` 持有编辑会话，保存仍由 Backend 使用预期修订 ID 防止并发覆盖。

### 3.3 页面与插件

当前主要页面位于 `views/home`、`views/scheduler`、`views/results` 和 `views/system`。工作流编辑器的节点材料由 Backend Schema 生成；运行详情通过 HTTP 加载事实并用 WebSocket 接收刷新提示。

`plugins/official` 包含内置插件 Vue 页面；`plugins/installed` 是外部插件安装目标；`plugins/sdk.ts` 只约定 `pages` 与 `resultPages` 的异步组件映射。插件页面仍运行在主应用权限和样式环境中。

## 4. 部署与发布

```text
deploy/
├─ production/
│  ├─ compose.yaml          生产单应用容器拓扑
│  ├─ deploy.sh             固定 digest 部署、migration、健康检查和回滚
│  ├─ runtime.env.example   非敏感运行配置模板
│  └─ README.md
└─ packages/
   └─ README.md             二进制发布包说明

scripts/
├─ verify.ps1 / verify.sh   本地聚合验证
└─ release/                 构建、扫描、清理、部署辅助及其 shell 测试
```

GitHub Actions 按 Backend、Frontend、插件和发布脚本路径选择检查。生产 Release/Deploy 只能通过显式授权触发；真实密钥不进入仓库或 Actions。

## 5. 文档职责

| 文档 | 负责内容 |
| --- | --- |
| `README.md` | 项目入口、能力、快速启动和文档索引 |
| `docs/architecture/overview.md` | 当前系统结构、数据流和边界 |
| `docs/contracts/README.md` | 已实现的跨模块接口与状态语义 |
| `docs/code-structure.md` | 源码目录、模块职责和修改入口 |
| `docs/plugin-development.md` | 第三方可信插件开发与生命周期 |
| `docs/user-guide.md` | 用户和管理员操作 |
| `docs/runbooks/*` | 可执行的开发、迁移、恢复和发布步骤 |
| `docs/architecture/decisions/*` | 已接受或被替代的架构决策历史 |
| `docs/quality/*`、`docs/templates/*` | 稳定门禁和证据模板 |

当前进度、Issue、PR、验收证据和放行状态只保存在 GitHub，不写回架构或契约文档。

## 6. 常见修改入口

| 需求 | 首要位置 | 通常同时检查 |
| --- | --- | --- |
| 新增或修改 HTTP API | `backend/internal/api` | 拥有状态的 Service、权限码、Frontend API、公共契约 |
| 修改工作流图或状态机 | `backend/internal/service/workflow*` | migration、API handler、编辑器、契约和恢复语义 |
| 新增核心控制节点 | `workflow_builtin.go`、`workflow_nodes.go` | 图校验、编辑器材料和契约 |
| 新增业务节点或策略 | 对应官方/外部插件 | SDK 注册、Schema、结果页、插件指南 |
| 修改认证或权限 | `security`、`service/auth.go`、`perm` | API 中间件、种子菜单、Frontend 指令和安全文档 |
| 修改数据库结构 | 新 migration | 模型、Service 事务、Runbook、备份与回滚 |
| 修改系统页面 | `frontend/src/views` | API、Router、菜单种子、权限与文案 |
| 修改插件生命周期 | `manifest`、`pluginbuild`、`pluginlifecycle` | CLI、生成文件、migration runner 和插件指南 |
| 修改部署 | `deploy/production`、`.github/workflows` | Release 脚本、Runbook、回滚与安全边界 |

先从状态拥有模块修改，再更新调用方。不要为单个调用引入新的共享层，也不要从 API、工作流核心或前端绕过插件和领域边界。
