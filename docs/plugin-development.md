# CoinSphere 插件开发指南

当前支持 Core 4 / SDK 4 / manifest schemaVersion 1。插件是经过审查的可信本地 Go/Vue 源码：安装复制源码、执行专属 migration、生成注册表并重建应用；启动只加载已编译且安装版本精确匹配的插件。插件拥有进程权限，SDK 不提供恶意代码沙箱。

## 可直接参考的普通业务插件

[业务事项示例](../examples/plugins/business/coinsphere-plugin.json)包含可编译 Go 节点、WorkflowScope 路由、固定 ResultPage、自定义 ack 动作和 Vue RunPanel，使用标准生成注册表接入；没有 Core 业务分支。示例只产生合成结果，ack 确认当前响应，不持久保存业务状态。增加实际领域数据时由插件自己的 migration、事务和幂等键负责。

```text
business/
├─ coinsphere-plugin.json
├─ backend/go.mod、plugin.go
├─ frontend/index.ts、TaskPanel.vue、TaskResults.vue
└─ migrations/00001_business.sql
```

Frontend 入口必须导出贡献对象，不能只 default export。manifest 路径使用插件根内相对路径，拒绝绝对路径、反斜杠和符号链接逃逸。backend.module 必须等于后端 go.mod 的 module。插件独立 module 在主仓库生成 require/replace 后编译，不手工改生成注册表。

## 清单、权限与注册

manifest 声明 id/name/version/sdkMajor/requiresCore/backend/frontend/migrations/contributes/permissions。requiresPlugins 为插件 ID 到 SemVer 约束的映射；依赖缺失、版本不满足或循环明确失败。

通用贡献有 nodes、triggers、apiRoutes、pages、resultPages、runPanels、cleanup、ingress、assistantQueries、workflowValidators、templates、migrations。除 migration 外，声明必须与实际注册一致。金融 provider 和 strategy 属于 plugin/contracts/trading 的领域注册，通用 SDK 不包含这些接口。

权限使用 `plugins.{pluginId}.{action}`；节点 ExecutionPermissions、页面 PermissionCode、路由 PermissionCode、ResultPage 的读取与 ActionPermissions 必须属于声明目录。重复插件/节点/贡献键及未声明权限拒绝注册。受保护权限只由超级管理员授予，Live 放行不可通过普通 execute 替代。

后端导出 `Register(sdk.Registrar, sdk.Host) error`。Host 提供插件 Store、受限网络、事件、收件箱、代理及实时提示；使用已有能力，不另建 Core 服务定位器。NodeDescriptor 的 Config/Input/Output Schema 使用 JSON Schema Draft 2020-12，UISchema 为 JSON 对象；SideEffect/State/Pool 必须符合实际行为。

## 节点语义

Action 返回合法 JSON 对象；分支节点输出 branch 必须属于声明 Branches。通用输出按节点 ID 访问，Core 不解释 ready/triggered/summary。Notification.compose 负责组合消息，Quant 负责策略与逐帧评估。

静态参数进入 Config，动态值来自 InputBindings。Secret 用 x-coinsphere-secret 标记且通过 SecretReader.Read 访问，不出现在 Config、输出、日志、报告或前端 props。State 只在 StatePersistent 节点使用，成功时与检查点同事务提交；其命名空间包含修订 ID，状态图串行执行。ArtifactStore 存储内容寻址制品。

外部 HTTP/AI 调用声明 SideEffectExternal；账本、通知和审批声明对应副作用。仅当实际 operation key、查询对账或数据库事务能证明安全重试时使用 RetrySafe；对端接收 Idempotency-Key 本身不构成证明。错误通过 sdk.ExecutionError 表达 permanent/transient/unknown_result；未知结果先对账或人工处理。诊断重放只复用副作用检查点。

Trigger 必须响应 Context 取消，通过 Emitter 提交标准事件。Loop 父节点不占子节点池；插件配置若包含 nodeInstanceId，提供 RewriteConfigNodeIDs，让展开 ID 与 Secret/State/来源映射一致。WorkflowValidator 检查插件领域约束，Core 只负责通用图不变量。

## 三种作用域

| Scope | 处理规则 |
| --- | --- |
| SystemScope | 使用 Core 注入的 WorkflowIDs/AllWorkflows；查询用 sdk.ScopeWorkflows，空范围必须返回空结果；长连接调用 SessionValid 复验 |
| WorkflowScope | 只访问固定 WorkflowID/RevisionID/NodeInstanceID/PluginID；不读取客户端扩大范围参数 |
| ResultScope | 只访问固定 Scope/Filters/Resources；动作既需视图 AllowedActions 又需当前能力；不接受客户端 userId |

ResultPage.Resources 提取所有 workflow/node 引用，ScopeSchema/FilterSchema 校验结构；领域节点约束使用 ValidateScope(ctx,tx,scope) 并利用给定事务读取。自定义动作在 Actions 与 ActionPermissions 注册，RouteDescriptor.Action 引用它。固定节点范围不能调用全 Run 或整工作流动作。若领域动作会持久写入，还需在所属事务复验当前视图、授权和领域幂等性。

Cleanup 使用 Core 提供的 tx 删除所属领域数据，不能另开事务提交部分清理；Ingress 验证插件自己的协议后产出 CloudEvent。日志只在拥有状态的模块记录受控分类。

## Vue 贡献契约

| 导出 | key 与 props |
| --- | --- |
| pages | PageDescriptor.pageKey，普通插件页面 |
| nodeEditors | 完整 nodeType；config、schema、uiSchema；发出 update(key,value) |
| nodeRenderers | 完整 nodeType；通用画布节点渲染 |
| runPanels | RunPanel.panelKey；`result:{run,runNode}` |
| resultPages | ResultPage.pageKey；view 与 `context:{viewId,pluginId,pageKey,allowedActions}` |
| resultConfigs | ResultPage.pageKey；v-model:scope/filters 和 scopeSchema/filterSchema |
| providerConfigComponents | 显式 provider key，调用方提供 pluginId |

每个值为 `() => import('./Component.vue')`。按明确归属直接加载，缺 key/缺编译组件须显示错误；不遍历试加载插件，不从插件深层导入 Core views。通用 Schema 表单在 frontend/src/components/workflow。读写按钮受当前能力与范围控制，保持标签、键盘操作和窄屏可用。

## migration 与生命周期

每个插件使用独立 schema_migrations_g4 账本，旧 SQL 字节不变、只追加更高版本。官方业务基线与 Core 分离。外部插件生成目录记录最新 migration 版本，启动在对应账本进行只读核对。插件迁移实际连接绑定 schema 与 PG session lock，不能修改共享连接池容量或遗留 search_path。

升级必须提高版本，且没有当前定义、在途运行或未撤销视图活动引用；跨 major 也遵循这些前提，不提供自动旧图兼容。安装/升级失败恢复源码、生成表、依赖和新增 migration；若 Down 被领域保护拒绝，保留失败原因并使用一致恢复点。

卸载保留 schema 和数据；历史引用阻止 purge-data。操作命令与维护窗口见[开发手册](runbooks/development.md)，数据库恢复见[迁移手册](runbooks/database-migrations.md)。CLI 构建完成不等于插件已加载，需用配对镜像重启后检查安装/编译/加载事实。

## 验证

默认交远端 CI：插件注册/依赖/贡献、Scope 正反例、真实引用阻断、失败升级恢复，以及普通业务示例的 Go 编译和 Vue 关键路径。领域节点还需保留最小行为检查，金融用合成行情/Decimal/UTC，外部网络使用替身。插件源码审查和真实环境恢复演练不能由这些合成用例代替。
