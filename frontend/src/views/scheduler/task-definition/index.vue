<!-- 任务定义管理页面组件：index.vue -->
<template>
  <div class="task-definition-page art-full-height">
    <ArtSearchBar
      v-model="formFilters"
      :items="formItems"
      :show-expand="false"
      @search="handleSearch"
      @reset="handleReset"
    />

    <ElCard class="art-table-card">
      <ArtTableHeader
        v-model:columns="columnChecks"
        :show-zebra="false"
        :loading="loading"
        @refresh="loadPageData"
      />

      <ArtTable
        :loading="loading"
        :columns="columns"
        :data="taskDefinitionPage.records"
        :pagination="{
          current: pagination.current,
          size: pagination.size,
          total: taskDefinitionPage.total
        }"
        :pagination-options="{
          pageSizes: [10, 20, 50],
          layout: 'total, prev, pager, next, sizes',
          align: 'center'
        }"
        :stripe="false"
        @pagination:size-change="handleSizeChange"
        @pagination:current-change="handleCurrentChange"
      />
    </ElCard>

    <ElDialog v-model="dialogVisible" destroy-on-close width="720px" :title="dialogTitle">
      <template v-if="currentRecord">
        <div class="dialog-header">
          <div class="dialog-header__title">{{ currentRecord.label }}</div>
          <div class="dialog-header__meta">{{ currentRecord.code }}</div>
          <div v-if="currentRecord.description" class="dialog-header__description">
            {{ currentRecord.description }}
          </div>
        </div>

        <ElAlert
          v-if="unsupportedFieldLabels.length"
          type="info"
          :closable="false"
          show-icon
          class="dialog-alert"
          :title="`以下字段当前仅展示 schema，不支持结构化编辑：${unsupportedFieldLabels.join('、')}`"
        />

        <ElEmpty
          v-if="!editableFields.length"
          description="当前任务定义没有可直接编辑的根级基础参数"
        />

        <ElForm v-else label-position="top" class="task-definition-form">
          <ElFormItem v-for="field in editableFields" :key="field.key" :label="field.title">
            <template #label>
              <div class="field-label">
                <span>{{ field.title }}</span>
                <ElTag v-if="field.hasSchemaDefault" size="small" effect="plain" type="info">
                  代码默认：{{ formatValue(field.schemaDefault) }}
                </ElTag>
              </div>
            </template>

            <ElSelect
              v-if="field.kind === 'select'"
              v-model="dialogForm[field.key]"
              clearable
              filterable
              style="width: 100%"
            >
              <ElOption
                v-for="option in field.options"
                :key="String(option)"
                :label="String(option)"
                :value="option"
              />
            </ElSelect>

            <ElSwitch v-else-if="field.kind === 'boolean'" v-model="dialogForm[field.key]" />

            <ElInputNumber
              v-else-if="field.kind === 'integer' || field.kind === 'number'"
              v-model="dialogForm[field.key]"
              :min="field.minimum"
              :max="field.maximum"
              :precision="field.kind === 'integer' ? 0 : undefined"
              controls-position="right"
              style="width: 100%"
            />

            <ElInput v-else v-model="dialogForm[field.key]" clearable />

            <div v-if="field.description" class="field-description">
              {{ field.description }}
            </div>
          </ElFormItem>
        </ElForm>
      </template>

      <template #footer>
        <ElButton @click="dialogVisible = false">取消</ElButton>
        <ElButton
          v-if="canUpdate && editableFields.length"
          type="primary"
          :loading="saveLoading"
          @click="handleSubmit"
        >
          保存
        </ElButton>
      </template>
    </ElDialog>
  </div>
</template>

<script setup lang="ts">
  import { Edit } from '@element-plus/icons-vue'
  import {
    ElButton,
    ElEmpty,
    ElInput,
    ElInputNumber,
    ElOption,
    ElSelect,
    ElSwitch,
    ElTag
  } from 'element-plus'
  import { useAuth } from '@/hooks/core/useAuth'
  import { useTableColumns } from '@/hooks/core/useTableColumns'
  import {
    fetchTaskDefinitionPage,
    fetchUpdateTaskDefinitionDefaultParams,
    type TaskDefinitionManagementItem,
    type TaskDefinitionManagementList,
    type TaskDefinitionQueryParams
  } from '@/api/scheduler'

  defineOptions({ name: 'TaskDefinitions' })

  type EditableFieldKind = 'string' | 'integer' | 'number' | 'boolean' | 'select'

  interface EditableField {
    key: string
    title: string
    kind: EditableFieldKind
    description: string
    minimum?: number
    maximum?: number
    options?: any[]
    schemaDefault?: any
    hasSchemaDefault: boolean
  }

  const { hasAuth } = useAuth()

  const loading = ref(false)
  const saveLoading = ref(false)
  const dialogVisible = ref(false)
  const currentRecord = ref<TaskDefinitionManagementItem | null>(null)
  const dialogForm = reactive<Record<string, any>>({})
  const taskDefinitionPage = ref<TaskDefinitionManagementList>({
    records: [],
    current: 1,
    size: 10,
    total: 0
  })

  const pagination = reactive({
    current: 1,
    size: 10
  })

  const initialFilters = {
    keyword: ''
  }

  const formFilters = reactive({ ...initialFilters })
  const canUpdate = computed(() => hasAuth('scheduler.task_definitions.update'))

  const formItems = computed(() => [
    {
      label: '关键词',
      key: 'keyword',
      type: 'input',
      props: {
        clearable: true,
        placeholder: '任务编码 / 名称 / 描述'
      }
    }
  ])

  const dialogTitle = computed(() => {
    if (!currentRecord.value) {
      return '编辑任务定义'
    }
    return `编辑任务定义：${currentRecord.value.label}`
  })

  const editableFields = computed(() =>
    buildEditableFields(currentRecord.value?.parameterSchema || {})
  )

  const unsupportedFieldLabels = computed(() => {
    const properties = currentRecord.value?.parameterSchema?.properties || {}
    if (!properties || typeof properties !== 'object') {
      return []
    }
    return Object.entries(properties)
      .filter(([, schema]) => resolveFieldKind(schema as Record<string, any>) === null)
      .map(([key, schema]) => String((schema as Record<string, any>).title || key))
  })

  const cloneValue = <T,>(value: T): T => {
    if (value === undefined || value === null) {
      return value
    }
    return JSON.parse(JSON.stringify(value)) as T
  }

  const formatValue = (value: any): string => {
    if (value === undefined || value === null || value === '') {
      return '--'
    }
    if (typeof value === 'object') {
      return JSON.stringify(value)
    }
    return String(value)
  }

  const summarizeParams = (params: Record<string, any>) => {
    const entries = Object.entries(params || {})
    if (!entries.length) {
      return '--'
    }
    return entries
      .slice(0, 4)
      .map(([key, value]) => `${key}=${formatValue(value)}`)
      .join('，')
  }

  const openEditDialog = (row: TaskDefinitionManagementItem) => {
    if (!canUpdate.value) {
      return
    }
    currentRecord.value = row
    Object.keys(dialogForm).forEach((key) => delete dialogForm[key])
    editableFields.value.forEach((field) => {
      const value = row.effectiveDefaultParams?.[field.key]
      dialogForm[field.key] =
        value === undefined ? cloneValue(field.schemaDefault) : cloneValue(value)
    })
    dialogVisible.value = true
  }

  const renderOperationButton = (row: TaskDefinitionManagementItem) => {
    if (!canUpdate.value) {
      return '--'
    }
    return h(
      ElButton,
      {
        circle: true,
        plain: true,
        size: 'small',
        type: 'primary',
        icon: Edit,
        title: '编辑',
        onClick: () => openEditDialog(row)
      },
      {}
    )
  }

  const { columns, columnChecks } = useTableColumns<TaskDefinitionManagementItem>(() => [
    {
      prop: 'label',
      label: '任务名称',
      minWidth: 180,
      align: 'center',
      showOverflowTooltip: true
    },
    {
      prop: 'code',
      label: '任务编码',
      minWidth: 180,
      align: 'center',
      showOverflowTooltip: true
    },
    {
      prop: 'description',
      label: '说明',
      minWidth: 220,
      align: 'center',
      showOverflowTooltip: true,
      formatter: (row) => row.description || '--'
    },
    {
      prop: 'effectiveDefaultParams',
      label: '当前全局默认参数',
      minWidth: 260,
      align: 'center',
      showOverflowTooltip: true,
      formatter: (row) => summarizeParams(row.effectiveDefaultParams || {})
    },
    {
      prop: 'updatedAt',
      label: '更新时间',
      minWidth: 170,
      align: 'center',
      formatter: (row) => row.updatedAt || '--'
    },
    {
      prop: 'operation',
      label: '操作',
      width: 90,
      align: 'center',
      formatter: (row) => renderOperationButton(row)
    }
  ])

  const loadPageData = async () => {
    loading.value = true
    try {
      taskDefinitionPage.value = await fetchTaskDefinitionPage({
        current: pagination.current,
        size: pagination.size,
        keyword: formFilters.keyword.trim() || undefined
      } satisfies TaskDefinitionQueryParams)
    } finally {
      loading.value = false
    }
  }

  const handleSearch = () => {
    pagination.current = 1
    void loadPageData()
  }

  const handleReset = () => {
    Object.assign(formFilters, { ...initialFilters })
    pagination.current = 1
    void loadPageData()
  }

  const handleCurrentChange = (current: number) => {
    pagination.current = current
    void loadPageData()
  }

  const handleSizeChange = (size: number) => {
    pagination.size = size
    pagination.current = 1
    void loadPageData()
  }

  const handleSubmit = async () => {
    if (!currentRecord.value) {
      return
    }

    const params: Record<string, any> = {}
    editableFields.value.forEach((field) => {
      if (dialogForm[field.key] === undefined) {
        return
      }
      params[field.key] = cloneValue(dialogForm[field.key])
    })

    saveLoading.value = true
    try {
      await fetchUpdateTaskDefinitionDefaultParams(currentRecord.value.code, { params })
      dialogVisible.value = false
      await loadPageData()
    } finally {
      saveLoading.value = false
    }
  }

  const resolveFieldKind = (schema: Record<string, any>): EditableFieldKind | null => {
    if (Array.isArray(schema?.enum) && schema.enum.length) {
      return 'select'
    }
    const schemaType = String(schema?.type || '').trim()
    if (schemaType === 'string') return 'string'
    if (schemaType === 'integer') return 'integer'
    if (schemaType === 'number') return 'number'
    if (schemaType === 'boolean') return 'boolean'
    return null
  }

  const buildEditableFields = (parameterSchema: Record<string, any>): EditableField[] => {
    const properties = parameterSchema?.properties
    if (!properties || typeof properties !== 'object') {
      return []
    }

    return Object.entries(properties).flatMap(([key, schema]) => {
      if (!schema || typeof schema !== 'object') {
        return []
      }
      const kind = resolveFieldKind(schema as Record<string, any>)
      if (!kind) {
        return []
      }
      return [
        {
          key,
          title: String((schema as Record<string, any>).title || key),
          kind,
          description: String((schema as Record<string, any>).description || ''),
          minimum:
            typeof (schema as Record<string, any>).minimum === 'number'
              ? Number((schema as Record<string, any>).minimum)
              : undefined,
          maximum:
            typeof (schema as Record<string, any>).maximum === 'number'
              ? Number((schema as Record<string, any>).maximum)
              : undefined,
          options: Array.isArray((schema as Record<string, any>).enum)
            ? [...(schema as Record<string, any>).enum]
            : undefined,
          schemaDefault: (schema as Record<string, any>).default,
          hasSchemaDefault: Object.prototype.hasOwnProperty.call(schema, 'default')
        }
      ]
    })
  }

  onMounted(() => {
    void loadPageData()
  })
</script>

<style scoped lang="scss">
  .task-definition-page {
    .dialog-header {
      margin-bottom: 16px;
    }

    .dialog-header__title {
      font-size: 16px;
      font-weight: 600;
      color: var(--art-text-gray-900);
    }

    .dialog-header__meta {
      margin-top: 4px;
      font-family: Consolas, 'Courier New', monospace;
      font-size: 13px;
      color: var(--art-text-gray-600);
    }

    .dialog-header__description {
      margin-top: 8px;
      line-height: 1.6;
      color: var(--art-text-gray-700);
    }

    .dialog-alert {
      margin-bottom: 16px;
    }

    .field-label {
      display: flex;
      flex-wrap: wrap;
      gap: 8px;
      align-items: center;
    }

    .field-description {
      margin-top: 6px;
      font-size: 12px;
      line-height: 1.5;
      color: var(--art-text-gray-600);
    }
  }
</style>
