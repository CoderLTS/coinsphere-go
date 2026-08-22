<template>
  <ElDrawer
    :model-value="modelValue"
    title="人工待办"
    size="min(520px, 100%)"
    destroy-on-close
    @update:model-value="$emit('update:modelValue', $event)"
  >
    <div class="action-drawer" v-loading="submitting">
      <div v-if="actions.length" class="action-drawer__list">
        <button
          v-for="action in actions"
          :key="action.id"
          type="button"
          :class="['action-drawer__item', { 'is-active': action.id === selectedId }]"
          @click="selectAction(action.id)"
        >
          <span class="action-drawer__risk"></span>
          <span class="action-drawer__copy">
            <strong>{{ action.title }}</strong>
            <small>{{ formatTime(action.createdAt) }} · {{ targetLabel(action) }}</small>
          </span>
          <ElTag v-if="action.requiresReauth" type="warning" effect="plain" size="small">
            需确认
          </ElTag>
        </button>
      </div>

      <ElEmpty v-else description="当前没有待处理动作" />

      <section v-if="selectedAction" class="action-drawer__detail">
        <header>
          <div>
            <span>待处理动作</span>
            <h3>{{ selectedAction.title }}</h3>
          </div>
          <ElTag type="warning" effect="plain">等待处理</ElTag>
        </header>

        <ElDescriptions :column="1" border size="small">
          <ElDescriptionsItem label="目标">{{ targetLabel(selectedAction) }}</ElDescriptionsItem>
          <ElDescriptionsItem label="过期时间">
            {{ selectedAction.expiresAt ? formatTime(selectedAction.expiresAt) : '不过期' }}
          </ElDescriptionsItem>
        </ElDescriptions>

        <ElAlert
          v-if="selectedAction.requiresReauth"
          type="warning"
          :closable="false"
          title="批准该动作需要重新验证当前账户。"
        />
        <ElAlert
          v-if="!canDecide"
          type="info"
          :closable="false"
          title="当前权限只允许查看该待办。"
        />

        <ElForm label-position="top">
          <WorkflowSchemaFields
            :schema="selectedAction.formSchema || emptySchema"
            :config="formData"
            @update="updateFormData"
          />
        </ElForm>

        <footer>
          <ElButton type="danger" plain :disabled="!canDecide" @click="decide('rejected')">
            拒绝
          </ElButton>
          <ElButton type="primary" :disabled="!canDecide" @click="decide('approved')">
            批准并继续
          </ElButton>
        </footer>
      </section>
    </div>
  </ElDrawer>
</template>

<script setup lang="ts">
  import { ElMessage, ElMessageBox } from 'element-plus'
  import { fetchReauth } from '@/api/auth'
  import {
    fetchDecideWorkflowAction,
    type WorkflowActionItem,
    type WorkflowActionDecisionPayload
  } from '@/api/scheduler'
  import WorkflowSchemaFields from '@/views/scheduler/workflow/editor/components/WorkflowSchemaFields.vue'
  import { useUserStore } from '@/store/modules/user'

  const props = defineProps<{
    modelValue: boolean
    actions: WorkflowActionItem[]
    initialActionId?: string
  }>()
  const emit = defineEmits<{
    (e: 'update:modelValue', value: boolean): void
    (e: 'decided', action: WorkflowActionItem): void
  }>()

  const emptySchema = { type: 'object', properties: {} }
  const userStore = useUserStore()
  const selectedId = ref('')
  const formData = ref<Record<string, any>>({})
  const submitting = ref(false)
  const selectedAction = computed(
    () => props.actions.find((action) => action.id === selectedId.value) || null
  )
  const canDecide = computed(
    () =>
      !selectedAction.value?.requiredPermission ||
      userStore.info.permissions.includes(selectedAction.value.requiredPermission)
  )

  const defaultsFromSchema = (schema: Record<string, any>) =>
    Object.fromEntries(
      Object.entries(schema?.properties || {}).map(([key, raw]) => {
        const field = (raw || {}) as Record<string, any>
        if (field.default !== undefined) return [key, field.default]
        if (field.type === 'boolean') return [key, false]
        if (field.type === 'array') return [key, []]
        if (field.type === 'object') return [key, {}]
        return [key, '']
      })
    )

  const selectAction = (id: string) => {
    selectedId.value = id
    const action = props.actions.find((item) => item.id === id)
    formData.value = defaultsFromSchema(action?.formSchema || emptySchema)
  }
  const updateFormData = (key: string, value: any) => {
    formData.value = { ...formData.value, [key]: value }
  }
  const formatTime = (value: string) =>
    value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '--'
  const targetLabel = (action: WorkflowActionItem) =>
    action.targetId ? `${action.targetType} · ${action.targetId}` : action.targetType || '系统'

  const decide = async (decision: WorkflowActionDecisionPayload['decision']) => {
    const action = selectedAction.value
    if (!action || !canDecide.value) return

    let reauthToken: string | undefined
    if (decision === 'approved' && action.requiresReauth) {
      const prompt = await ElMessageBox.prompt('请输入当前账户密码', '重新验证', {
        inputType: 'password',
        inputPlaceholder: '当前密码',
        confirmButtonText: '验证并批准',
        cancelButtonText: '取消',
        inputValidator: (value) => Boolean(value?.trim()) || '请输入当前密码'
      }).catch(() => null)
      if (!prompt) return
      reauthToken = (await fetchReauth(prompt.value)).reauthToken
    }

    submitting.value = true
    try {
      const result = await fetchDecideWorkflowAction(
        action.id,
        { decision, formData: decision === 'approved' ? formData.value : {} },
        reauthToken
      )
      ElMessage.success(decision === 'approved' ? '动作已批准。' : '动作已拒绝。')
      emit('decided', result)
    } finally {
      submitting.value = false
    }
  }

  watch(
    () => [props.modelValue, props.initialActionId, props.actions] as const,
    ([visible, initialActionId]) => {
      if (!visible) return
      const nextId =
        (initialActionId && props.actions.some((item) => item.id === initialActionId)
          ? initialActionId
          : props.actions[0]?.id) || ''
      if (nextId && nextId !== selectedId.value) selectAction(nextId)
      if (!nextId) selectedId.value = ''
    },
    { immediate: true, deep: true }
  )
</script>

<style scoped lang="scss">
  .action-drawer {
    display: flex;
    flex-direction: column;
    gap: 18px;
  }

  .action-drawer__list {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .action-drawer__item {
    display: grid;
    grid-template-columns: 4px minmax(0, 1fr) auto;
    gap: 10px;
    align-items: center;
    width: 100%;
    min-height: 58px;
    padding: 8px 10px;
    color: var(--art-gray-900);
    text-align: left;
    cursor: pointer;
    background: var(--el-fill-color-blank);
    border: 1px solid var(--art-card-border);
    border-radius: 6px;

    &:hover,
    &.is-active {
      border-color: #d69a2d;
    }
  }

  .action-drawer__risk {
    align-self: stretch;
    background: #d69a2d;
    border-radius: 2px;
  }

  .action-drawer__copy {
    display: flex;
    flex-direction: column;
    gap: 4px;
    min-width: 0;

    strong,
    small {
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }

    small {
      color: var(--art-gray-600);
    }
  }

  .action-drawer__detail {
    display: flex;
    flex-direction: column;
    gap: 16px;
    padding-top: 18px;
    border-top: 1px solid var(--art-card-border);

    header,
    footer {
      display: flex;
      gap: 12px;
      align-items: center;
      justify-content: space-between;
    }

    header span {
      font-size: 12px;
      color: var(--art-gray-600);
    }

    h3 {
      margin: 4px 0 0;
      font-size: 18px;
      letter-spacing: 0;
    }

    footer {
      justify-content: flex-end;
    }
  }
</style>
