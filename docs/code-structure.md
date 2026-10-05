# CoinSphere 代码结构

系统采用 Go/Vue 模块化单体，公共模型为 Graph 3，插件契约为 SDK 4。设计见[架构](architecture/overview.md)，跨模块语义见[契约](contracts/README.md)。

| 目录/文件 | 所有权与修改入口 |
| --- | --- |
| backend/main.go | 组装配置、schema 验证、注册、能力/导航种子、执行器、HTTP 和关闭 |
| backend/cmd/migrate | generation 4 Core/官方插件基线 CLI |
| backend/cmd/workflow-migrate | 一次性 inspect/plan/apply/verify，服务器源/目标配置 |
| backend/cmd/coinsphere | 可信源码插件 validate/install/upgrade/uninstall/purge-data |
| backend/internal/api | 信任边界解析、身份/能力、三种插件 Scope 路由、WebSocket、Problem Details |
| backend/internal/service/authorization\*.go、auth.go | 有效能力、资源授权、授予上限、事务复验与持久会话撤销 |
| backend/internal/service/workflow.go、workflow_publish.go、workflow_secrets.go | 原子定义/修订/metadata/Secret、独立发布指针、引用维护 |
| backend/workflow/graph | 通用图、Schema、CEL、命名空间绑定及纯函数；SDK 与 Core 共用 |
| backend/internal/service/workflow_run.go | 持久队列、租约、Run 预算、节点池、Loop、分类重试、诊断、状态/检查点事务 |
| backend/internal/service/workflow_human_tasks.go | durable wait、决定、替代、过期、取消的统一事务锁顺序 |
| backend/internal/service/workflow_events.go、workflow_triggers.go | CloudEvents、Outbox、入口、连续流和调度 |
| backend/internal/service/result_views.go | 固定范围结果、主体授权、自定义动作与当前视图复验 |
| backend/internal/service/notification.go | 通用站内收件箱和当前用户已读状态 |
| backend/internal/service/assistant\*.go、ai_models.go | 有界助手上下文、只读插件查询、授权草稿创建与模型配置 |
| backend/internal/migration/generation4 | 当前 Core 基线；sql/00001–00023 仅保留旧源演练，不用于当前 Up |
| backend/internal/workflowmigration | 只读快照、转换/映射、计划哈希、原子导入、幂等和验证 |
| backend/plugin/sdk | 通用 Action/Trigger、Schema、Permission、Scope、Ingress/Cleanup/RunPanel 等贡献 |
| backend/plugin/contracts/trading | 金融接口与领域 provider/strategy 注册；不进入通用 SDK |
| backend/plugin/official | AI、Connector、Notification、QQ、Quant、Binance；领域表及逐帧逻辑归插件 |
| backend/internal/pluginbuild、pluginlifecycle、pluginregistry | 静态表生成、引用锁、安装/编译失败恢复、生成后端表 |
| backend/version/builtin.json | 官方版本、依赖与贡献的唯一目录 |
| frontend/src/api/workflows.ts | 原生 Workflow/Revision/Run 传输与类型，无旧图或合成 ID 适配 |
| frontend/src/components/workflow | 通用 Schema 表单、Graph/Canvas/Loop、绑定编辑及状态标签 |
| frontend/src/views/workbench、results | 个人待办/工作流摘要和通用固定结果中心 |
| frontend/src/views/scheduler | 定义摘要、原生编辑器、运行列表与固定 Run 详情 |
| frontend/src/plugins | 前端 SDK、官方贡献和生成注册表；Quant 分析与 Binance 结果配置归插件 |
| examples/plugins/business | 实际非金融 Go/Vue 示例，只在 CI 的示例装配中加载 |
| backend/internal/testdb、\*\_test.go、frontend/e2e | 合成隔离行为/事务/竞态/迁移及关键页面证据 |

插件源码安装在 backend/plugin/installed 和 frontend/src/plugins/installed，生成 registry 不手工维护。插件数据由专属 schema/migration 拥有；Core 清理通过 Cleanup 同事务调用。公共契约、migration、依赖文件和官方目录由一个维护者统筹，最终只读复审由主 Agent 完成。

API 负责外部输入校验，服务/领域拥有状态与事务不变量，数据库约束保护持久事实。节点输出、日志、报告和 UI 不含秘密值。动态进度、PR 检查和启用证据保存在 GitHub；文档描述最终实现。
