# Workflow Graph v3 契约

## 顶层结构

```json
{
  "schemaVersion": 3,
  "profileRefs": [],
  "nodes": [],
  "edges": []
}
```

`profileRefs` 是修订中实际使用的 Profile 引用集合。每个引用包含 `pluginId`、`profileId`、`version` 和 `type`，版本必须是插件已发布的不可变版本。

## 节点与连线

节点使用 `nodeInstanceId`、`nodeType`、`nodeVersion`、`config`、可选的 `profileBindings`、可选的 `inputBindings` 和画布 `position`。输入绑定的 `kind` 只有 `node`、`event`、`profile`、`literal` 四种。

边只包含 `edgeId`、源节点与源端口、目标节点与目标端口，以及可选的展示标签：

```json
{
  "edgeId": "rsi-to-transition",
  "sourceNodeInstanceId": "rsi",
  "sourcePort": "true",
  "targetNodeInstanceId": "transition",
  "targetPort": "in"
}
```

边不保存条件文本。`core.condition`、`core.switch`、`core.join`、`core.transition`、`core.debounce`、`core.throttle`、`core.time_window` 和 `core.deduplicate` 负责结构化控制；`core.expression` 是显式高级节点。

## 运行语义

修订激活时，所有 Trigger 实例都有独立的运行时状态、租约、错误和有界重试。事件、定时、行情、Webhook、手动和回放入口各自创建 Run，Run 保存 `entryNodeInstanceId`、`triggerInstanceId`、`triggerEventId` 和 `profileSnapshot`。共享节点只等待当前 Run 中可达且已经命中的输入。

手动运行请求必须带 `revisionId`、`triggerNodeId` 和 JSON 对象形式的 `input`。Core 只允许选择声明 `manualTrigger` 的入口。

## Profile 生命周期

插件通过 SDK 注册 `ProfileDescriptor` 和 `ProfileProvider`，并提供 Profile 列表、草稿、复制、发布、停用和删除页面。工作流编辑器显示 Profile 名称、摘要和发布版本；修订只保存稳定引用，Core 不解析插件配置。停用只影响新修订引用，旧修订在运行时仍使用其固定版本。
