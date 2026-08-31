<!-- 工作流编辑器页面或组件：WorkflowNodeEditorCard。 -->
<template>
  <div class="node-editor-card" @pointerdown.stop @click.stop>
    <ElButton
      class="node-editor-card__commit"
      type="primary"
      native-type="button"
      @click="handleRequestCommit"
    >
      保存
    </ElButton>

    <div class="node-editor-card__scroll">
      <div class="node-editor-card__body">
        <ElAlert
          v-for="issue in issues"
          :key="issue.id"
          class="node-editor-card__issue"
          type="error"
          :closable="false"
          :title="issue.message"
        />

        <ElAlert
          v-for="(error, index) in errors"
          :key="`draft-${index}`"
          class="node-editor-card__issue"
          type="warning"
          :closable="false"
          :title="error"
        />

        <ElForm label-position="top">
          <ElFormItem label="节点名称">
            <ElInput
              v-model="localForm.label"
              placeholder="请输入节点名称"
              @blur="handleTextBlur(['label'])"
            />
          </ElFormItem>

          <ElFormItem label="节点类型">
            <ElInput :model-value="nodeTypeLabel" disabled />
          </ElFormItem>

          <template v-if="localForm.kind === 'start'">
            <ElAlert type="info" :closable="false" :title="startNodeHint" />

            <ElFormItem label="入口名称">
              <ElInput
                v-model="localForm.config.displayName"
                placeholder="用于运行时入口展示"
                @blur="handleTextBlur(['config', 'displayName'])"
              />
            </ElFormItem>

            <ElFormItem label="默认输入绑定 JSON">
              <ElInput
                v-model="startInputBindingsJson"
                type="textarea"
                :rows="4"
                placeholder='{"source":"manual"}'
                @blur="normalizeJsonObjectField('inputBindings', startInputBindingsJson)"
              />
            </ElFormItem>

            <template v-if="localForm.typeCode === 'start.schedule'">
              <ElFormItem label="计划类型">
                <ElSelect v-model="localForm.config.scheduleType" @change="emitModel">
                  <ElOption label="Cron" value="cron" />
                  <ElOption label="间隔执行" value="interval" />
                </ElSelect>
              </ElFormItem>

              <ElFormItem v-if="localForm.config.scheduleType === 'cron'" label="Cron 表达式">
                <ElInput
                  v-model="localForm.config.cronExpression"
                  placeholder="例如 0 */5 * * * *"
                  @blur="handleTextBlur(['config', 'cronExpression'])"
                />
              </ElFormItem>

              <ElFormItem v-if="localForm.config.scheduleType === 'cron'" label="时区">
                <ElInput
                  v-model="localForm.config.timeZone"
                  placeholder="例如 Asia/Shanghai"
                  @blur="handleTextBlur(['config', 'timeZone'])"
                />
              </ElFormItem>

              <template v-else-if="localForm.config.scheduleType === 'interval'">
                <ElFormItem label="间隔数值">
                  <ElInputNumber
                    v-model="localForm.config.value"
                    class="node-editor-card__full"
                    :min="1"
                    :step="1"
                    @change="emitModel"
                  />
                </ElFormItem>

                <ElFormItem label="间隔单位">
                  <ElSelect v-model="localForm.config.unit" @change="emitModel">
                    <ElOption label="秒" value="seconds" />
                    <ElOption label="分钟" value="minutes" />
                    <ElOption label="小时" value="hours" />
                    <ElOption label="天" value="days" />
                  </ElSelect>
                </ElFormItem>
              </template>
            </template>

            <template v-else-if="localForm.typeCode === 'start.event'">
              <ElFormItem label="事件类型">
                <ElInput
                  v-model="localForm.config.eventType"
                  placeholder="例如 news.items.synced"
                  @blur="handleTextBlur(['config', 'eventType'])"
                />
              </ElFormItem>

              <ElFormItem label="过滤条件 JSON">
                <ElInput
                  v-model="eventFiltersJson"
                  type="textarea"
                  :rows="4"
                  placeholder='[{"path":"payload.source","equals":"blockbeats"}]'
                  @blur="normalizeJsonArrayField('filters', eventFiltersJson)"
                />
              </ElFormItem>
            </template>

            <template v-else-if="localForm.typeCode === 'start.webhook'">
              <ElAlert
                type="info"
                :closable="false"
                title="Webhook Secret 会在工作流版本激活后由运行态生成并管理，这里只配置声明式入口。"
              />
            </template>
          </template>

          <template v-else-if="localForm.kind === 'agent'">
            <ElFormItem label="智能体">
              <ElSelect
                v-model="localForm.config.agentCode"
                placeholder="请选择智能体"
                filterable
                clearable
                @change="emitModel"
              >
                <ElOption
                  v-for="item in agentOptions"
                  :key="item.code"
                  :label="item.label"
                  :value="item.code"
                />
              </ElSelect>
              <div v-if="selectedAgent" class="node-editor-card__field-hint">
                数据源：{{ selectedAgent.dataSourceLabel || selectedAgent.dataSourceType }}
              </div>
            </ElFormItem>

            <ElFormItem v-if="selectedAgent?.supportsAnalyze" label="使用数据源的结构化分析模板">
              <ElSwitch v-model="localForm.config.analyze" @change="emitModel" />
              <div class="node-editor-card__field-hint">
                开启后忽略下面的提示词，改用该数据源自带的分析指令。
              </div>
            </ElFormItem>

            <ElFormItem v-if="!localForm.config.analyze" label="提示词">
              <ElInput
                v-model="localForm.config.promptTemplate"
                type="textarea"
                :rows="5"
                :placeholder="promptTemplatePlaceholder"
                @blur="handleTextBlur(['config', 'promptTemplate'])"
              />
            </ElFormItem>

            <ElFormItem v-if="selectedAgent?.requiresRefId" label="关联数据 id 路径">
              <ElInput
                v-model="localForm.config.refIdPath"
                placeholder="例如 currentItem.id"
                @blur="handleTextBlur(['config', 'refIdPath'])"
              />
              <div class="node-editor-card__field-hint">
                该智能体需要关联数据（{{ selectedAgent.dataSourceLabel }}），从共享状态的这个路径取
                id。
              </div>
            </ElFormItem>

            <ElFormItem label="结果写入共享状态的键名">
              <ElInput
                v-model="localForm.config.outputKey"
                placeholder="默认 agentResult"
                @blur="handleTextBlur(['config', 'outputKey'])"
              />
              <div class="node-editor-card__field-hint">{{ outputKeyHint }}</div>
            </ElFormItem>

            <ElFormItem label="指定模型配置 id">
              <ElInputNumber
                v-model="localForm.config.modelConfigId"
                class="node-editor-card__full"
                :min="0"
                @change="emitModel"
              />
              <div class="node-editor-card__field-hint">
                留空或 0 表示用该智能体绑定的模型；工作流按创建者的模型配置解析。
              </div>
            </ElFormItem>
          </template>

          <template v-else-if="localForm.kind === 'condition'">
            <ElFormItem label="字段路径">
              <ElInput
                v-model="localForm.config.path"
                placeholder="例如 taskResult.insertedCount"
                @blur="handleTextBlur(['config', 'path'])"
              />
            </ElFormItem>

            <ElFormItem label="比较运算">
              <ElSelect v-model="localForm.config.operator" @change="emitModel">
                <ElOption label="等于" value="eq" />
                <ElOption label="不等于" value="ne" />
                <ElOption label="包含" value="contains" />
                <ElOption label="大于" value="gt" />
                <ElOption label="大于等于" value="gte" />
                <ElOption label="小于" value="lt" />
                <ElOption label="小于等于" value="lte" />
                <ElOption label="Truthy" value="truthy" />
              </ElSelect>
            </ElFormItem>

            <ElFormItem label="比较值">
              <ElInput
                v-model="localForm.config.value"
                placeholder="用于与实际值比较"
                @blur="handleTextBlur(['config', 'value'])"
              />
            </ElFormItem>

            <ElFormItem label="比较值路径">
              <ElInput
                v-model="localForm.config.valuePath"
                placeholder="选填；填了就取共享状态里该路径的值，优先于上面的固定比较值"
                @blur="handleTextBlur(['config', 'valuePath'])"
              />
            </ElFormItem>

            <ElAlert
              type="info"
              :closable="false"
              title="需要多个条件时在下面添加；一旦填了多条件，上面的单条件就不再生效。"
            />
            <WorkflowSchemaFields
              :schema="configSchema"
              :ui-schema="uiSchema"
              :config="localForm.config"
              :keys="['logic', 'conditions']"
              @update="handleSchemaFieldUpdate"
            />
          </template>

          <template v-else-if="localForm.kind === 'indicator-condition'">
            <QuantIndicatorEditor
              :schema="configSchema"
              :ui-schema="uiSchema"
              :config="localForm.config"
              @update="handleSchemaFieldUpdate"
            />
          </template>

          <template v-else-if="localForm.kind === 'foreach'">
            <WorkflowSchemaFields
              :schema="configSchema"
              :config="localForm.config"
              @update="handleSchemaFieldUpdate"
            />
            <ElAlert
              type="info"
              :closable="false"
              title="BODY 连线是循环体（每个元素跑一遍）；要在遍历结束后继续，请从 NEXT 端口再拉一条连线。"
            />
          </template>

          <template v-else-if="localForm.kind === 'notify'">
            <ElFormItem label="通知标题">
              <ElInput
                v-model="localForm.config.title"
                placeholder="请输入通知标题"
                @blur="handleTextBlur(['config', 'title'])"
              />
            </ElFormItem>

            <WorkflowSchemaFields
              :schema="configSchema"
              :config="localForm.config"
              :keys="['subjectKey', 'message']"
              @update="handleSchemaFieldUpdate"
            />

            <ElAlert
              type="info"
              :closable="false"
              title="未选择目标时通知工作流创建者；用户与角色可同时选择并自动去重。"
            />

            <ElAlert
              v-if="props.notifyOptionsLoading"
              type="info"
              :closable="false"
              title="正在加载用户和角色列表..."
            />

            <div class="target-panel">
              <div class="target-panel__header">
                <strong>通知目标</strong>
                <ElButton
                  size="small"
                  plain
                  native-type="button"
                  :disabled="props.notifyOptionsLoading || !canAddNotifyTarget"
                  @click="addNotifyTarget"
                >
                  添加目标
                </ElButton>
              </div>

              <div v-if="notifyTargetRows.length" class="target-panel__list">
                <div
                  v-for="(target, index) in notifyTargetRows"
                  :key="target.rowId"
                  class="target-panel__item"
                >
                  <div class="target-panel__item-header">
                    <div class="target-panel__title">
                      <span class="target-panel__index">目标 {{ index + 1 }}</span>
                      <span class="target-panel__title-hint">
                        {{ target.targetType === 'role' ? '按角色接收通知' : '按用户接收通知' }}
                      </span>
                    </div>
                    <ElButton
                      class="target-panel__remove"
                      size="small"
                      plain
                      type="danger"
                      native-type="button"
                      @click="removeNotifyTarget(target.rowId)"
                    >
                      删除
                    </ElButton>
                  </div>

                  <div class="target-panel__item-grid">
                    <div class="target-panel__field-group target-panel__field-group--type">
                      <div class="target-panel__field-label">目标类型</div>
                      <ElSelect
                        v-model="target.targetType"
                        class="target-panel__field target-panel__field--type"
                        @change="handleNotifyTargetTypeChange(target.rowId, target.targetType)"
                      >
                        <ElOption
                          label="用户"
                          value="user"
                          :disabled="isNotifyTargetTypeDisabled(target.rowId, 'user')"
                        />
                        <ElOption
                          label="角色"
                          value="role"
                          :disabled="isNotifyTargetTypeDisabled(target.rowId, 'role')"
                        />
                      </ElSelect>
                    </div>

                    <div class="target-panel__field-group target-panel__field-group--targets">
                      <div class="target-panel__field-label">目标对象</div>
                      <ElSelect
                        v-model="target.targetIds"
                        class="target-panel__field target-panel__field--targets"
                        multiple
                        filterable
                        clearable
                        popper-class="target-panel__select-popper"
                        :loading="props.notifyOptionsLoading"
                        :disabled="props.notifyOptionsLoading"
                        :placeholder="target.targetType === 'role' ? '请选择角色' : '请选择用户'"
                        @change="handleNotifyTargetIdsChange(target.rowId, target.targetIds)"
                      >
                        <ElOption
                          v-for="option in resolveNotifyTargetOptions(target.targetType)"
                          :key="`${target.targetType}-${option.value}`"
                          :label="option.label"
                          :value="option.value"
                        >
                          <span class="target-panel__option-label">{{ option.label }}</span>
                        </ElOption>
                      </ElSelect>
                    </div>
                  </div>
                </div>
              </div>

              <ElEmpty v-else description="未选择目标，将通知工作流创建者" :image-size="40" />
            </div>
          </template>

          <template v-else-if="localForm.kind === 'event'">
            <WorkflowSchemaFields
              :schema="configSchema"
              :config="localForm.config"
              @update="handleSchemaFieldUpdate"
            />
          </template>

          <template v-else-if="localForm.kind === 'http'">
            <WorkflowSchemaFields
              :schema="configSchema"
              :config="localForm.config"
              @update="handleSchemaFieldUpdate"
            />
          </template>

          <template v-else-if="localForm.kind === 'delay'">
            <WorkflowSchemaFields
              :schema="configSchema"
              :config="localForm.config"
              @update="handleSchemaFieldUpdate"
            />
          </template>

          <template v-else-if="isQuantCandleNode">
            <QuantCandleConfigEditor
              :schema="configSchema"
              :ui-schema="uiSchema"
              :config="localForm.config"
              @update="handleSchemaFieldUpdate"
            />
          </template>

          <template v-else-if="localForm.typeCode === 'official.quant.code_strategy'">
            <WorkflowSchemaFields
              :schema="configSchema"
              :ui-schema="uiSchema"
              :config="localForm.config"
              :keys="['series', 'parameters', 'booleanOutputs', 'decimalOutputs', 'branchField']"
              @update="handleSchemaFieldUpdate"
            />
            <ElFormItem label="CEL 代码">
              <QuantCodeStrategyEditor
                :model-value="String(localForm.config.source || '')"
                :config="localForm.config"
                @update:model-value="handleSchemaFieldUpdate('source', $event)"
              />
            </ElFormItem>
          </template>

          <template v-else-if="localForm.kind === 'end'">
            <ElAlert
              type="success"
              :closable="false"
              title="结束节点没有额外配置，执行链路会在此终止。"
            />
          </template>

          <!-- 兜底：没有定制表单的节点按后端下发的 configSchema 自动渲染。
               新增一种节点只要后端登记好 schema，这里就有可用的表单，不必再改本文件。 -->
          <template v-else>
            <WorkflowSchemaFields
              :schema="configSchema"
              :ui-schema="uiSchema"
              :config="localForm.config"
              @update="handleSchemaFieldUpdate"
            />
          </template>
        </ElForm>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
  import type { WorkflowAgentOption } from '@/api/scheduler'
  import type {
    WorkflowDomainNode,
    WorkflowEditorIssue,
    WorkflowNodeFormModel,
    WorkflowNotifyTargetOption
  } from '../types'
  import { getNodeConfigSchema, getNodeUISchema } from '../node-registry'
  import QuantCandleConfigEditor from './QuantCandleConfigEditor.vue'
  import QuantCodeStrategyEditor from './QuantCodeStrategyEditor.vue'
  import QuantIndicatorEditor from './QuantIndicatorEditor.vue'
  import WorkflowSchemaFields from './WorkflowSchemaFields.vue'

  interface Props {
    node: WorkflowDomainNode
    model: WorkflowNodeFormModel | null
    agentOptions?: WorkflowAgentOption[]
    notifyUserOptions?: WorkflowNotifyTargetOption[]
    notifyRoleOptions?: WorkflowNotifyTargetOption[]
    notifyOptionsLoading?: boolean
    issues: WorkflowEditorIssue[]
    errors?: string[]
  }

  interface Emits {
    (e: 'update:model', value: WorkflowNodeFormModel): void
    (e: 'request-commit'): void
    (e: 'request-discard'): void
    (e: 'request-close'): void
    (e: 'request-remove'): void
  }

  interface NotifyTargetRow {
    rowId: string
    targetType: 'user' | 'role'
    targetIds: number[]
  }

  const props = withDefaults(defineProps<Props>(), {
    errors: () => [],
    agentOptions: () => [],
    notifyUserOptions: () => [],
    notifyRoleOptions: () => [],
    notifyOptionsLoading: false
  })
  const emit = defineEmits<Emits>()

  const NODE_TYPE_LABELS: Record<string, string> = {
    'start.manual': '开始节点（手动触发）',
    'start.schedule': '开始节点（定时触发）',
    'start.event': '开始节点（事件触发）',
    'start.webhook': '开始节点（Webhook 触发）',
    'condition.branch': '条件判断节点',
    'official.quant.volume_spike_condition': '放量判断节点',
    'official.quant.price_change_condition': '价格波动判断节点',
    'official.quant.macd_condition': 'MACD 判断节点',
    'official.quant.kdj_condition': 'KDJ 判断节点',
    'official.quant.rsi_condition': 'RSI 判断节点',
    'official.quant.bollinger_condition': '布林带判断节点',
    'official.quant.market_signal': '输出信号节点',
    'official.quant.backtest_start': '回测开始节点',
    'official.quant.code_strategy': '代码策略节点',
    'official.quant.position': '仓位计算节点',
    'official.quant.output_signal': '输出策略信号节点',
    'official.notification.in_app': '站内通知节点',
    'official.notification.dingtalk': '钉钉通知节点',
    'official.qq.receive': 'QQ 消息接收节点',
    'official.qq.send': 'QQ 消息发送节点',
    'official.notification.smtp': '邮件通知节点',
    foreach: '遍历节点',
    notify: '通知节点',
    'event.publish': '发布事件节点',
    'http.request': 'HTTP 请求节点',
    'delay.wait': '等待节点',
    end: '结束节点'
  }

  const NODE_KIND_LABELS: Record<string, string> = {
    start: '开始节点',
    condition: '判断节点',
    'indicator-condition': '量化指标判断节点',
    foreach: '遍历节点',
    notify: '通知节点',
    event: '事件节点',
    http: 'HTTP 节点',
    delay: '等待节点',
    end: '结束节点'
  }

  const NOTIFY_TARGET_TYPES = ['user', 'role'] as const
  const cloneModel = (value: WorkflowNodeFormModel | null): WorkflowNodeFormModel => ({
    id: value?.id || props.node.id,
    label: value?.label || props.node.data.title,
    typeCode: value?.typeCode || props.node.data.typeCode,
    kind: value?.kind || props.node.data.kind,
    config: JSON.parse(JSON.stringify(value?.config || props.node.data.config || {}))
  })

  const localForm = reactive<WorkflowNodeFormModel>(cloneModel(props.model))
  const notifyTargetRows = ref<NotifyTargetRow[]>([])
  const startInputBindingsJson = ref('{}')
  const eventFiltersJson = ref('[]')
  const localModelSnapshot = ref('')
  const lastEmittedSnapshot = ref('')

  const nodeTypeLabel = computed(
    () => NODE_TYPE_LABELS[localForm.typeCode] || NODE_KIND_LABELS[localForm.kind] || '工作流节点'
  )

  // 后端下发的配置 schema：HTTP / 延迟 / 事件 / 遍历这几种「字段直译」的节点直接按它渲染表单，
  // 不再在本文件里逐个手写一遍。开始 / 任务 / 通知 / 条件有联动和自定义控件，仍走下面的定制模板。
  const configSchema = computed(() => getNodeConfigSchema(localForm.typeCode))
  const uiSchema = computed(() => getNodeUISchema(localForm.typeCode))
  const isQuantCandleNode = computed(() =>
    ['official.quant.realtime_candles', 'official.quant.backfill_candles'].includes(
      localForm.typeCode
    )
  )

  /** 当前选中的智能体：决定要不要显示「关联数据 id 路径」和「结构化分析」开关。 */
  const selectedAgent = computed(
    () => props.agentOptions.find((item) => item.code === localForm.config.agentCode) || null
  )

  // 这两段文案里带 {{ }}，写在模板里会被 Vue 当成插值解析，所以放到脚本里当普通字符串。
  const promptTemplatePlaceholder = '支持 {{ 路径 }} 引用共享状态，例如 {{ currentItem.title }}'
  const outputKeyHint = '下游节点可用 {{ 键名.content }} 引用回复正文。'

  const startNodeHint = computed(() => {
    if (localForm.typeCode === 'start.schedule') {
      return '定时开始节点只声明计划配置，真正注册状态由运行态统一管理。'
    }
    if (localForm.typeCode === 'start.event') {
      return '事件开始节点只声明监听事件和过滤条件，命中一次就会创建一条独立 Run。'
    }
    if (localForm.typeCode === 'start.webhook') {
      return 'Webhook 开始节点只声明入口，Secret 在版本激活后由运行态生成和轮换。'
    }
    return '手动开始节点用于声明可供人工启动的入口。'
  })

  const createNotifyTargetRow = (value?: Partial<NotifyTargetRow>): NotifyTargetRow => ({
    rowId: `notify-target-${Math.random().toString(36).slice(2, 10)}`,
    targetType: NOTIFY_TARGET_TYPES.includes(value?.targetType as 'user' | 'role')
      ? (value?.targetType as 'user' | 'role')
      : 'user',
    targetIds: Array.isArray(value?.targetIds)
      ? Array.from(
          new Set(
            value.targetIds
              .map((item) => Number(item))
              .filter((item) => Number.isInteger(item) && item > 0)
          )
        )
      : []
  })

  const normalizeNotifyRowsFromConfig = (value: unknown): NotifyTargetRow[] => {
    if (!Array.isArray(value)) return []
    const grouped = new Map<'user' | 'role', number[]>()

    value.forEach((item) => {
      if (!item || typeof item !== 'object') return
      const target = item as Record<string, unknown>
      const targetType = String(target.targetType || '').trim() as 'user' | 'role'
      const targetIdValue = Number(target.targetId)
      if (!NOTIFY_TARGET_TYPES.includes(targetType)) return
      if (!Number.isInteger(targetIdValue) || targetIdValue <= 0) return
      const current = grouped.get(targetType) || []
      current.push(targetIdValue)
      grouped.set(targetType, current)
    })

    return Array.from(grouped.entries()).map(([targetType, targetIds]) =>
      createNotifyTargetRow({
        targetType,
        targetIds
      })
    )
  }

  const syncJsonTextRefs = () => {
    startInputBindingsJson.value = JSON.stringify(
      localForm.config.inputBindings &&
        typeof localForm.config.inputBindings === 'object' &&
        !Array.isArray(localForm.config.inputBindings)
        ? localForm.config.inputBindings
        : {},
      null,
      2
    )
    eventFiltersJson.value = JSON.stringify(
      Array.isArray(localForm.config.filters) ? localForm.config.filters : [],
      null,
      2
    )
  }

  const syncNotifyRowsToConfig = () => {
    if (localForm.kind !== 'notify') return
    localForm.config.targets = notifyTargetRows.value.flatMap((item) =>
      item.targetIds.map((targetId) => ({
        targetType: item.targetType,
        targetId
      }))
    )
  }

  const syncNotifyConfigToRows = () => {
    if (localForm.kind !== 'notify') {
      notifyTargetRows.value = []
      return
    }

    localForm.config.title = String(localForm.config.title ?? '')
    notifyTargetRows.value = normalizeNotifyRowsFromConfig(localForm.config.targets)
    syncNotifyRowsToConfig()
  }

  const usedNotifyTargetTypes = computed(
    () => new Set(notifyTargetRows.value.map((item) => item.targetType))
  )
  const canAddNotifyTarget = computed(
    () => usedNotifyTargetTypes.value.size < NOTIFY_TARGET_TYPES.length
  )

  const resolveNotifyTargetOptions = (targetType: 'user' | 'role') =>
    targetType === 'role' ? props.notifyRoleOptions : props.notifyUserOptions

  const updateLocalForm = (value: WorkflowNodeFormModel | null) => {
    const next = cloneModel(value)
    localForm.id = next.id
    localForm.label = next.label
    localForm.typeCode = next.typeCode
    localForm.kind = next.kind
    localForm.config = next.config
    syncNotifyConfigToRows()
    syncJsonTextRefs()
    const snapshot = JSON.stringify(cloneModel(localForm))
    localModelSnapshot.value = snapshot
    lastEmittedSnapshot.value = snapshot
  }

  const emitModel = () => {
    syncNotifyRowsToConfig()
    const snapshot = JSON.stringify(cloneModel(localForm))
    if (snapshot === lastEmittedSnapshot.value) return
    localModelSnapshot.value = snapshot
    lastEmittedSnapshot.value = snapshot
    emit('update:model', cloneModel(localForm))
  }

  const handleRequestCommit = () => {
    emitModel()
    emit('request-commit')
  }

  const normalizeTextAtPath = (path: string[]) => {
    let cursor: Record<string, any> = localForm as any
    for (let index = 0; index < path.length - 1; index += 1) {
      cursor = cursor?.[path[index]]
      if (!cursor) return
    }
    const finalKey = path[path.length - 1]
    if (typeof cursor?.[finalKey] === 'string') {
      cursor[finalKey] = cursor[finalKey].trim()
    }
    emitModel()
  }

  const handleTextBlur = (path: string[]) => {
    normalizeTextAtPath(path)
  }

  const normalizeJsonObjectField = (targetKey: string, sourceValue: string) => {
    const text = String(sourceValue || '').trim()
    if (!text) {
      localForm.config[targetKey] = {}
      syncJsonTextRefs()
      emitModel()
      return
    }
    try {
      const parsed = JSON.parse(text)
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
        throw new Error('invalid object')
      }
      localForm.config[targetKey] = parsed
    } catch {
      syncJsonTextRefs()
      return
    }
    syncJsonTextRefs()
    emitModel()
  }

  const normalizeJsonArrayField = (targetKey: string, sourceValue: string) => {
    const text = String(sourceValue || '').trim()
    if (!text) {
      localForm.config[targetKey] = []
      syncJsonTextRefs()
      emitModel()
      return
    }
    try {
      const parsed = JSON.parse(text)
      if (!Array.isArray(parsed)) {
        throw new Error('invalid array')
      }
      localForm.config[targetKey] = parsed
    } catch {
      syncJsonTextRefs()
      return
    }
    syncJsonTextRefs()
    emitModel()
  }

  /** schema 驱动的字段改动统一走这里写回草稿：子组件不直接改 config，改动来源单一好追。 */
  const handleSchemaFieldUpdate = (key: string, value: any) => {
    localForm.config[key] = value
    emitModel()
  }

  const addNotifyTarget = () => {
    const nextType = NOTIFY_TARGET_TYPES.find((item) => !usedNotifyTargetTypes.value.has(item))
    if (!nextType) return
    notifyTargetRows.value = [
      ...notifyTargetRows.value,
      createNotifyTargetRow({ targetType: nextType, targetIds: [] })
    ]
    syncNotifyRowsToConfig()
    emitModel()
  }

  const removeNotifyTarget = (rowId: string) => {
    notifyTargetRows.value = notifyTargetRows.value.filter((item) => item.rowId !== rowId)
    syncNotifyRowsToConfig()
    emitModel()
  }

  const isNotifyTargetTypeDisabled = (rowId: string, targetType: 'user' | 'role') =>
    notifyTargetRows.value.some((item) => item.rowId !== rowId && item.targetType === targetType)

  const handleNotifyTargetTypeChange = (rowId: string, value: string) => {
    const nextType = NOTIFY_TARGET_TYPES.includes(value as 'user' | 'role')
      ? (value as 'user' | 'role')
      : 'user'
    const currentRow = notifyTargetRows.value.find((item) => item.rowId === rowId) || null
    if (!currentRow) return
    if (isNotifyTargetTypeDisabled(rowId, nextType)) {
      notifyTargetRows.value = notifyTargetRows.value.map((item) =>
        item.rowId === rowId ? { ...item, targetType: currentRow.targetType } : item
      )
      return
    }
    notifyTargetRows.value = notifyTargetRows.value.map((item) =>
      item.rowId === rowId ? { ...item, targetType: nextType, targetIds: [] } : item
    )
    syncNotifyRowsToConfig()
    emitModel()
  }

  const handleNotifyTargetIdsChange = (rowId: string, value: number[]) => {
    notifyTargetRows.value = notifyTargetRows.value.map((item) =>
      item.rowId === rowId
        ? {
            ...item,
            targetIds: Array.from(
              new Set(
                (Array.isArray(value) ? value : [])
                  .map((entry) => Number(entry))
                  .filter((entry) => Number.isInteger(entry) && entry > 0)
              )
            )
          }
        : item
    )
    syncNotifyRowsToConfig()
    emitModel()
  }

  watch(
    () => [props.node.id, JSON.stringify(cloneModel(props.model))],
    ([, incomingSnapshot]) => {
      if (incomingSnapshot === localModelSnapshot.value) return
      updateLocalForm(props.model)
    },
    { immediate: true }
  )
</script>

<style scoped lang="scss">
  .node-editor-card {
    --el-bg-color: var(--workflow-overlay-bg, #f4f3ee);
    --el-fill-color-blank: var(--workflow-overlay-raised, #fbfaf6);
    --el-fill-color-light: var(--workflow-overlay-soft, #e7e6e0);
    --el-border-color-light: var(--workflow-overlay-border-soft, #c8c7c1);
    --el-border-color-lighter: var(--workflow-overlay-border-subtle, #d7d5ce);
    --el-text-color-primary: var(--workflow-overlay-text, #17191b);
    --el-text-color-regular: var(--workflow-overlay-regular, #34383a);
    --el-text-color-secondary: var(--workflow-overlay-muted, #6d7477);
    --el-text-color-placeholder: var(--workflow-overlay-placeholder, #8a8f91);
    --el-disabled-bg-color: var(--workflow-overlay-soft-2, #e1e0da);
    --el-disabled-text-color: var(--workflow-overlay-muted, #6d7477);

    position: relative;
    display: flex;
    flex-direction: column;
    width: 100%;
    height: 100%;
    min-height: 0;
    overflow: hidden;
    background: var(--workflow-overlay-bg, #f4f3ee);
    border: 1px solid var(--workflow-overlay-border, #4b5256);
    border-radius: 8px;
    box-shadow: 0 12px 30px rgb(31 35 48 / 0.12);
  }

  .node-editor-card__commit {
    position: absolute;
    top: 10px;
    right: 10px;
    z-index: 3;
    height: 28px;
    min-height: 28px;
    padding: 0 12px;
    font-size: 12px;
    font-weight: 600;
    color: #fff;
    letter-spacing: 0;
    background: var(--theme-color);
    border: 1px solid var(--theme-color);
    border-radius: 6px;
    box-shadow: none;

    &:hover {
      background: var(--el-color-primary-light-3);
      box-shadow: none;
      transform: translateY(-1px);
    }

    &:active {
      box-shadow: none;
      transform: translateY(0);
    }
  }

  .node-editor-card__field-hint {
    margin-top: 6px;
    font-size: 12px;
    line-height: 18px;
    color: var(--workflow-overlay-muted, #6d7477);
    word-break: break-all;
  }

  .node-editor-card__scroll {
    flex: 1;
    min-height: 0;
    overflow: hidden auto;
    overscroll-behavior: contain;
  }

  .node-editor-card__body {
    min-height: min-content;
    padding: 42px 12px 12px;
  }

  .node-editor-card__issue {
    margin-bottom: 10px;
  }

  .node-editor-card__full {
    width: 100%;
  }

  .node-editor-card__param-control {
    display: flex;
    gap: 10px;
    align-items: center;
    width: 100%;
  }

  .node-editor-card__param-input {
    flex: 1;
    width: 100%;
  }

  .node-editor-card__param-boolean {
    display: flex;
    gap: 10px;
    align-items: center;
  }

  .target-panel {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 6px 8px;
    background: var(--workflow-overlay-soft, #e7e6e0);
    border: 1px solid var(--workflow-overlay-border-soft, #c8c7c1);
    border-radius: 2px;
  }

  .target-panel__header {
    display: flex;
    gap: 12px;
    align-items: center;
    justify-content: space-between;
  }

  .target-panel__list {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .target-panel__item {
    display: flex;
    flex-direction: column;
    gap: 10px;
    padding: 8px;
    background: var(--workflow-overlay-raised, #f9f8f4);
    border: 1px solid var(--workflow-overlay-border-subtle, #cbc9c2);
    border-radius: 2px;
  }

  .target-panel__item-header {
    display: flex;
    gap: 12px;
    align-items: flex-start;
    justify-content: space-between;
  }

  .target-panel__item-grid {
    display: grid;
    grid-template-columns: 92px minmax(0, 1fr);
    gap: 10px;
    align-items: start;
  }

  .target-panel__index {
    font-size: 14px;
    font-weight: 600;
    line-height: 1.3;
    color: var(--el-text-color-primary);
  }

  .target-panel__field {
    width: 100%;
  }

  .target-panel__title {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }

  .target-panel__title-hint {
    font-size: 12px;
    line-height: 1.4;
    color: var(--el-text-color-secondary);
  }

  .target-panel__field-group {
    display: flex;
    flex-direction: column;
    gap: 6px;
    min-width: 0;
  }

  .target-panel__field-group--type {
    width: 92px;
  }

  .target-panel__field-label {
    font-size: 12px;
    line-height: 1.4;
    color: var(--el-text-color-secondary);
  }

  .target-panel__field--type {
    max-width: 92px;
  }

  .target-panel__field--targets {
    min-width: 0;
  }

  .target-panel__remove {
    flex-shrink: 0;
  }

  .target-panel__option-label {
    display: block;
    line-height: 18px;
    word-break: break-word;
    white-space: normal;
  }

  @media (width <= 768px) {
    .target-panel__item-grid {
      grid-template-columns: 1fr;
    }
  }

  pre {
    margin: 0;
    word-break: break-word;
    white-space: pre-wrap;
  }

  :deep(.el-form-item) {
    margin-bottom: 12px;
  }

  :deep(.el-textarea__inner) {
    max-height: 160px;
  }

  :deep(.target-panel__field .el-select__tags) {
    max-width: calc(100% - 36px);
  }

  :deep(.target-panel__field--targets .el-select__wrapper) {
    align-items: flex-start;
    height: auto;
    min-height: 40px;
    padding-top: 6px;
    padding-bottom: 6px;
  }

  :deep(.target-panel__field .el-tag) {
    max-width: 100%;
  }

  :deep(.target-panel__field .el-tag__content) {
    line-height: 16px;
    word-break: break-word;
    white-space: normal;
  }

  :deep(.target-panel__select-popper .el-select-dropdown__item) {
    height: auto;
    min-height: 34px;
    padding-top: 8px;
    padding-bottom: 8px;
    line-height: 18px;
    white-space: normal;
  }
</style>
