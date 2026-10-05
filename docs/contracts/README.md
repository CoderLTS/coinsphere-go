# 公共契约

本契约对应 Core 4、SDK major 4、Graph schemaVersion 3 和 PostgreSQL 16。旧 API/图没有在线兼容层；离线转换见[迁移手册](../runbooks/database-migrations.md)。

## HTTP 与身份

成功响应为 `{code:200,msg:"",data:...}`，错误为 RFC 9457 Problem Details，包含 type/title/status/detail/requestId。ID 是真实数据库 ID；UTC 时间使用 RFC 3339，金融值使用十进制字符串。

除登录、健康、静态资源与凭据验证的 Ingress 外，API 使用 Bearer access token。登出持久撤销当前 tokenId。有效能力来自已启用角色及独立 role_permissions；菜单布局不会改变能力。受保护操作在服务端事务内再次校验身份和授权。

| 接口 | 语义 |
| --- | --- |
| POST /api/v1/workflows | graph/template、metadata、secretChanges 和运行配置原子创建；返回真实工作流与初始 draft 指针 |
| GET /api/v1/workflows | 分页摘要，含最近 Run 状态和有效操作权限；不要求前端逐行取详情 |
| POST /api/v1/workflows/{id}/revisions | expectedDraftRevisionId、graph、可选 metadata/secretChanges 原子保存；过期指针返回冲突 |
| POST /api/v1/workflows/{id}/publish | revisionId、expectedPublishedRevisionId 显式发布；必要凭据与执行能力不满足时拒绝 |
| POST /api/v1/workflows/{id}/lifecycle | activate/deactivate 等生命周期操作；自动触发绑定发布定义 |
| POST /api/v1/workflows/{id}/runs | revisionId、entryPoint、input 创建固定快照运行；手工试运行不切换发布指针 |
| GET/POST /api/v1/workflow-runs/{runId} | 查询真实 Run 与节点尝试；授权后的 cancel/retry/replay |
| GET /api/v1/human-tasks、POST /api/v1/human-tasks/{taskId} | 授权范围内待办列表；一次性 approve/reject，已决定/过期返回冲突 |
| GET/PUT /api/v1/workflows/{id}/grants | owner/user/role 资源授权；授予范围不得超过操作者 |
| GET /api/v1/workbench | 当前用户工作流、待办与结果摘要 |
| GET /api/v1/plugins/catalog | 已加载插件的结果页和运行面板目录；节点另由 /workflows/node-definitions 提供 |
| /api/v1/result-views | 固定范围视图、用户/角色授权、开放/停用/撤销；revoked 不可重新开放 |

没有工作流资源范围的请求不可读取 Run、日志、审批或制品。相同 SHA256 的制品仍须验证当前用户可访问的引用。系统观察要求 system.observe，助手与模型配置分别要求 assistant.use/config.ai.manage。

## Graph 3

图由 schemaVersion、entryPoints、nodes、edges 组成。entryPoints 使用通用名字，main 是默认入口；每个入口指向有效节点，不要求 realtime/backtest 固定名称。节点保留 nodeInstanceId/nodeType/nodeVersion/config/inputBindings/position，版本精确校验。

| Binding.kind | 取值规则                                          |
| ------------ | ------------------------------------------------- |
| literal      | value 是完整 JSON 值，Decimal 必须为字符串        |
| field        | nodeInstanceId 与 fieldPath 从指定节点输出取值    |
| input        | fieldPath 从入口输入取值                          |
| cel          | expression 显式访问 input、nodes、event、incoming |

CEL 示例：`nodes["check"].ready && input.amount == "1.25"`。输出按节点 ID 命名空间访问。incoming 只包含到达该节点的实际已选边及来源输出；边 condition、来源端口和声明 branch 共同决定可达性。未选分支跳过，汇合节点执行一次，不对 ready/triggered 做隐式判断。

Config 只存静态配置；Secret 字段以 ConfigSchema 的 x-coinsphere-secret 声明，正文通过 secretChanges 独立提交，不进入图。修订查询只返回 secretFields 是否已配置。替换、保留或移除凭据随保存事务完成；发布和运行校验 required Secret。

Loop config 包含 maxIterations、timeoutSeconds、exitCondition 和 body。循环体用 core.loop_item/core.loop_end，展开节点 ID 为父ID.子ID；退出表达式中的 input 是当前 iteration/value。循环体不能持久等待，父节点不占子节点槽，绝对期限沿用最早 attempt。

## Run、错误与状态

Run 状态原样返回 queued/running/waiting/retrying/succeeded/failed/cancelled；等待与重试有独立语义。RunNode 保留 attempt/loopIteration/invocationStarted 和受控摘要。修订与检查点不可变，被 Run 引用的旧修订不会阻断正常保存。

租约 token、有效期限与授权保护开始调用、状态、检查点、日志及终态写入。永久错误不重试，transient 在节点声明安全重试时最多三次，unknown_result 阻断自动/人工直接重试。HTTP/AI 调用按外部副作用处理；对端 Idempotency-Key 本身不证明幂等。诊断复用原副作用检查点，缺证据失败。

审批创建与 waiting 同事务；决定、取消、过期和替代原子恢复 Run，不会产生已决定任务仍永久 waiting。持久状态按 workflow/revision/node 隔离，含状态图全工作流串行提交。

## SDK 4 与贡献

ActionRequest 提供 Revision、NodeInstanceID、OperationKey、Input、Config、Secrets、State、Artifacts、Incoming、GraphSnapshot 与 Logger；ActionResult 提供 Output/Artifacts。TriggerRequest 与 Emitter 用于持久事件，Trigger 需响应取消。金融类型和逐帧执行不属于通用 SDK。

PluginDescriptor 声明权限与 contributes，RouteDescriptor/PageDescriptor/ResultPageDescriptor/节点执行必须引用所属插件声明的权限。manifest 保持 schemaVersion 1，sdkMajor 为 4；官方版本与依赖来自 backend/version/builtin.json。

| Scope | 路由与约束 |
| --- | --- |
| SystemScope | /api/v1/plugins/{pluginId}/\*；需要声明能力，WorkflowIDs/AllWorkflows 由 Core 注入，查询用 sdk.ScopeWorkflows 过滤 |
| WorkflowScope | /api/v1/workflows/{workflowId}/nodes/{nodeInstanceId}/plugins/{pluginId}/\*；锁定当前修订、节点归属与资源授权 |
| ResultScope | /api/v1/result-views/{viewId}/plugins/{pluginId}/\*；锁定 Resources、Scope、Filters、AllowedActions、当前 UserID；动作还检查 ActionPermissions |

ResultScope.HumanTasks 捕获当前身份和视图范围。固定节点范围不能执行整 Run 的 retry/cancel 或工作流 pause。系统/结果路由处理器不得接受客户端 userId、scope、workflowIds 扩大范围。

RunPanel 通过 panelKey/nodeTypes 注册，组件接收 `{result:{run,runNode}}`；resultPages 组件接收 view 与 PluginResultContext；resultConfigs 接收 scope/filters 双向模型及 Schema。nodeEditors 使用 config 与 update(key,value)，UI Schema 通用表单由 Core 提供。模块按显式 pluginId/贡献类型/key 加载，缺组件有可见错误。

Cleanup 运行在 Core 提供的同一数据库事务；Ingress 验证领域协议后产出 CloudEvent；RewriteConfigNodeIDs 重写 Loop 配置内真实节点 ID。插件日志只在状态拥有层记录，不包含凭据、个人数据或原始载荷。

## 生命周期与数据库

安装、编译、加载三个事实分开，安装版本与编译版本不相等则不加载。必需依赖与重复贡献注册明确失败。现行 draft/published、在途 Run、未撤销视图维护活动引用；建立引用与升级/卸载锁同一安装行。升级只允许版本递增且旧 migration 字节不变，并要求先解除全部活动引用；跨 major 不提供兼容假象。

卸载保留数据和历史引用；purge-data 要求已卸载、所有引用解除、匹配确认文本。失败安装/升级恢复源码、注册表、依赖文件和新增 migration，回滚本身失败需人工恢复点处理。插件是可信同进程代码，Scope 是契约边界，不是恶意代码隔离。
