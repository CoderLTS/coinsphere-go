<!-- 工作流定义列表。 -->
<template>
  <div class="workflow-definition-page art-full-height">
    <ArtSearchBar
      v-model="formFilters"
      :items="formItems"
      :show-expand="false"
      @search="handleSearch"
      @reset="handleReset"
    />

    <ElCard class="art-table-card">
      <div class="workflow-library">
        <aside class="workflow-groups" aria-label="工作流分组">
          <div class="workflow-groups__header">
            <span>分组</span>
            <ElTooltip
              v-if="hasAuth('scheduler.workflow_definitions.create')"
              content="新建分组"
              placement="top"
            >
              <ElButton text circle :icon="Plus" aria-label="新建分组" @click="createGroup" />
            </ElTooltip>
          </div>

          <div class="workflow-groups__scroll">
            <div class="workflow-groups__fixed">
              <button
                type="button"
                class="workflow-group-filter"
                :class="{ 'is-active': selectedGroup === 'all' }"
                @click="selectGroup('all')"
              >
                <ElIcon><FolderOpened /></ElIcon>
                <span class="workflow-group-filter__name">全部</span>
                <span class="workflow-group-filter__count">{{ definitions.length }}</span>
              </button>
              <button
                type="button"
                class="workflow-group-filter"
                :class="{ 'is-active': selectedGroup === 'ungrouped' }"
                @click="selectGroup('ungrouped')"
              >
                <ElIcon><Folder /></ElIcon>
                <span class="workflow-group-filter__name">未分组</span>
                <span class="workflow-group-filter__count">{{ ungroupedCount }}</span>
              </button>
            </div>

            <VueDraggable
              v-model="workflowGroups"
              class="workflow-groups__custom"
              handle=".workflow-group-filter__drag"
              :animation="150"
              :disabled="!hasAuth('scheduler.workflow_definitions.update')"
              @end="persistGroupOrder"
            >
              <div v-for="(group, index) in workflowGroups" :key="group.id" class="workflow-group">
                <ElTooltip
                  v-if="hasAuth('scheduler.workflow_definitions.update')"
                  content="拖动排序"
                  placement="top"
                >
                  <ElButton
                    text
                    circle
                    class="workflow-group-filter__drag"
                    :icon="Rank"
                    :aria-label="`拖动分组 ${group.name}`"
                  />
                </ElTooltip>
                <button
                  type="button"
                  class="workflow-group-filter workflow-group-filter--custom"
                  :class="{ 'is-active': selectedGroup === group.id }"
                  @click="selectGroup(group.id)"
                >
                  <ElIcon><Folder /></ElIcon>
                  <span class="workflow-group-filter__name">{{ group.name }}</span>
                  <span class="workflow-group-filter__count">{{ groupCount(group.id) }}</span>
                </button>
                <ElDropdown
                  v-if="
                    hasAuth('scheduler.workflow_definitions.update') ||
                    hasAuth('scheduler.workflow_definitions.delete')
                  "
                  trigger="click"
                  @command="(command) => handleGroupCommand(command, group, index)"
                >
                  <ElButton
                    text
                    circle
                    :icon="MoreFilled"
                    :aria-label="`管理分组 ${group.name}`"
                    @click.stop
                  />
                  <template #dropdown>
                    <ElDropdownMenu>
                      <ElDropdownItem
                        v-if="hasAuth('scheduler.workflow_definitions.update')"
                        command="rename"
                      >
                        重命名
                      </ElDropdownItem>
                      <ElDropdownItem
                        v-if="hasAuth('scheduler.workflow_definitions.update')"
                        command="up"
                        :disabled="index === 0"
                      >
                        上移
                      </ElDropdownItem>
                      <ElDropdownItem
                        v-if="hasAuth('scheduler.workflow_definitions.update')"
                        command="down"
                        :disabled="index === workflowGroups.length - 1"
                      >
                        下移
                      </ElDropdownItem>
                      <ElDropdownItem
                        v-if="hasAuth('scheduler.workflow_definitions.delete')"
                        command="delete"
                        divided
                      >
                        删除分组
                      </ElDropdownItem>
                    </ElDropdownMenu>
                  </template>
                </ElDropdown>
              </div>
            </VueDraggable>
          </div>
        </aside>

        <section class="workflow-table">
          <ArtTableHeader
            :loading="loading"
            :show-zebra="false"
            layout="refresh,size,fullscreen,settings"
            @refresh="loadPageData"
          >
            <template #left>
              <ElSpace wrap>
                <ElButton
                  v-if="hasAuth('scheduler.workflow_definitions.create')"
                  type="primary"
                  @click="openCreateWorkflow"
                >
                  新建工作流
                </ElButton>
                <ElDropdown
                  v-if="
                    selectedDefinitions.length && hasAuth('scheduler.workflow_definitions.update')
                  "
                  trigger="click"
                  @command="assignSelectedToGroup"
                >
                  <ElButton :loading="assigning">
                    移至分组（{{ selectedDefinitions.length }}）<ElIcon class="el-icon--right"
                      ><ArrowDown
                    /></ElIcon>
                  </ElButton>
                  <template #dropdown>
                    <ElDropdownMenu>
                      <ElDropdownItem :command="0">未分组</ElDropdownItem>
                      <ElDropdownItem
                        v-for="group in workflowGroups"
                        :key="group.id"
                        :command="group.id"
                      >
                        {{ group.name }}
                      </ElDropdownItem>
                    </ElDropdownMenu>
                  </template>
                </ElDropdown>
              </ElSpace>
            </template>
          </ArtTableHeader>

          <ArtTable
            ref="tableRef"
            row-key="id"
            :loading="loading"
            :data="filteredDefinitions"
            :stripe="false"
            table-layout="auto"
            empty-height="320px"
            @selection-change="handleSelectionChange"
          >
            <ElTableColumn
              v-if="hasAuth('scheduler.workflow_definitions.update')"
              type="selection"
              width="48"
              reserve-selection
            />
            <ElTableColumn
              prop="displayName"
              label="工作流名称"
              min-width="240"
              show-overflow-tooltip
            />
            <ElTableColumn label="分组" min-width="180">
              <template #default="{ row }">
                <ElSelect
                  v-if="hasAuth('scheduler.workflow_definitions.update')"
                  :model-value="row.groupId || 0"
                  size="small"
                  :disabled="assigning"
                  @change="(groupId) => assignDefinitions([row], Number(groupId) || null)"
                >
                  <ElOption label="未分组" :value="0" />
                  <ElOption
                    v-for="group in workflowGroups"
                    :key="group.id"
                    :label="group.name"
                    :value="group.id"
                  />
                </ElSelect>
                <span v-else>{{ groupName(row.groupId) }}</span>
              </template>
            </ElTableColumn>
            <ElTableColumn label="版本" width="100" align="center">
              <template #default="{ row }">v{{ row.version }}</template>
            </ElTableColumn>
            <ElTableColumn label="状态" width="110" align="center">
              <template #default="{ row }">
                <ElTag :type="statusTagType(row.workflowStatus)" effect="plain">
                  {{ statusLabel(row.workflowStatus) }}
                </ElTag>
              </template>
            </ElTableColumn>
            <ElTableColumn label="创建时间" min-width="180">
              <template #default="{ row }">{{ formatDateTime(row.createdAt) }}</template>
            </ElTableColumn>
            <ElTableColumn label="操作" width="220" align="center">
              <template #default="{ row }">
                <ElSpace size="small">
                  <ElTooltip content="工作流日志" placement="top">
                    <ElButton
                      circle
                      plain
                      size="small"
                      type="primary"
                      :icon="Clock"
                      @click="openLogs(row)"
                    />
                  </ElTooltip>
                  <ElTooltip content="编辑" placement="top">
                    <ElButton
                      circle
                      plain
                      size="small"
                      type="primary"
                      :icon="Edit"
                      @click="router.push(`/scheduler/workflow/${row.id}/edit`)"
                    />
                  </ElTooltip>
                  <ElTooltip content="版本记录" placement="top">
                    <ElButton
                      circle
                      plain
                      size="small"
                      type="primary"
                      :icon="Collection"
                      @click="openVersionDialog(row)"
                    />
                  </ElTooltip>
                  <ElTooltip :content="lifecycleLabel(row.workflowStatus)" placement="top">
                    <ElButton
                      circle
                      plain
                      size="small"
                      :type="row.workflowStatus === 'inactive' ? 'success' : 'warning'"
                      :icon="SwitchButton"
                      :loading="actingId === row.id"
                      @click="toggleLifecycle(row)"
                    />
                  </ElTooltip>
                  <ElTooltip
                    :content="isBacktestWorkflow(row) ? '运行回测' : '手动运行'"
                    placement="top"
                  >
                    <ElButton
                      circle
                      plain
                      size="small"
                      type="primary"
                      :icon="VideoPlay"
                      :disabled="!isBacktestWorkflow(row) && row.workflowStatus !== 'active'"
                      :loading="runningId === row.id"
                      @click="runWorkflow(row)"
                    />
                  </ElTooltip>
                </ElSpace>
              </template>
            </ElTableColumn>
          </ArtTable>
        </section>
      </div>
    </ElCard>

    <ElDialog v-model="createVisible" title="新建工作流" width="min(520px, calc(100vw - 32px))">
      <ElForm label-position="top">
        <ElFormItem label="工作流名称">
          <ElInput v-model="createForm.name" maxlength="120" show-word-limit />
        </ElFormItem>
        <ElFormItem label="模板">
          <ElSelect v-model="createForm.templateKey" class="backtest-form__full">
            <ElOption
              v-for="template in workflowTemplates"
              :key="template.key"
              :label="template.name"
              :value="template.key"
            />
          </ElSelect>
        </ElFormItem>
        <ElFormItem label="分组">
          <ElSelect v-model="createForm.groupId" class="backtest-form__full">
            <ElOption label="未分组" :value="0" />
            <ElOption
              v-for="group in workflowGroups"
              :key="group.id"
              :label="group.name"
              :value="group.id"
            />
          </ElSelect>
        </ElFormItem>
        <div v-if="selectedTemplate?.description" class="create-template-description">
          {{ selectedTemplate.description }}
        </div>
      </ElForm>
      <template #footer>
        <ElButton @click="createVisible = false">取消</ElButton>
        <ElButton type="primary" :loading="creating" @click="submitCreateWorkflow">创建</ElButton>
      </template>
    </ElDialog>

    <ElDialog
      v-model="versionDialogVisible"
      title="版本记录"
      width="min(920px, calc(100vw - 32px))"
    >
      <div>
        <div v-if="versionDetail" class="version-header">
          <div>
            <div class="version-header__title">{{ versionDetail.displayName }}</div>
            <div v-if="versionDetail.description" class="version-header__description">
              {{ versionDetail.description }}
            </div>
          </div>
          <ElTag effect="plain" type="info">{{ versionRows.length }} 个版本</ElTag>
        </div>

        <ElTable :data="versionRows" empty-text="暂无版本记录">
          <ElTableColumn label="版本" width="100" align="center">
            <template #default="{ row }">
              <ElTag :type="row.isLatest ? 'primary' : 'info'" effect="plain">
                v{{ row.version }}
              </ElTag>
            </template>
          </ElTableColumn>
          <ElTableColumn label="状态" min-width="220">
            <template #default="{ row }">
              <ElSpace size="small" wrap>
                <ElTag v-if="row.isLatest" type="primary" effect="plain">最新版本</ElTag>
                <ElTag v-if="row.isActive" type="success" effect="plain">当前激活</ElTag>
                <ElTag v-if="!row.isLatest && !row.isActive" type="info" effect="plain">
                  历史版本
                </ElTag>
              </ElSpace>
            </template>
          </ElTableColumn>
          <ElTableColumn prop="executionCount" label="执行数" width="100" align="center" />
          <ElTableColumn label="创建时间" min-width="180">
            <template #default="{ row }">{{ formatDateTime(row.createdAt) }}</template>
          </ElTableColumn>
          <ElTableColumn
            v-if="
              hasAuth('scheduler.workflow_definitions.update') ||
              hasAuth('scheduler.workflow_definitions.delete')
            "
            label="操作"
            width="100"
            align="center"
          >
            <template #default="{ row }">
              <ElSpace size="small">
                <ElTooltip
                  v-if="hasAuth('scheduler.workflow_definitions.update')"
                  content="打开版本"
                  placement="top"
                >
                  <ElButton
                    circle
                    plain
                    size="small"
                    type="primary"
                    :icon="Edit"
                    @click="openVersionEditor(row)"
                  />
                </ElTooltip>
                <ElTooltip
                  v-if="hasAuth('scheduler.workflow_definitions.delete') && !row.isLatest"
                  :content="row.executionCount ? '已有执行记录，不能删除' : '删除版本'"
                  placement="top"
                >
                  <ElButton
                    circle
                    plain
                    size="small"
                    type="danger"
                    :icon="Delete"
                    :disabled="row.executionCount > 0"
                    :loading="deletingVersionId === row.id"
                    aria-label="删除版本"
                    @click="deleteVersion(row)"
                  />
                </ElTooltip>
              </ElSpace>
            </template>
          </ElTableColumn>
        </ElTable>
      </div>
    </ElDialog>

    <ElDialog v-model="backtestVisible" title="运行回测" width="min(560px, calc(100vw - 32px))">
      <ElAlert
        v-if="backtestError"
        class="backtest-form__error"
        type="error"
        show-icon
        :closable="false"
        :title="backtestError"
      />
      <ElForm label-position="top">
        <ElFormItem label="策略版本">
          <ElSelect v-model="backtestForm.definitionId" class="backtest-form__full">
            <ElOption
              v-for="revision in backtestWorkflow?.versions || []"
              :key="revision.id"
              :label="`v${revision.version}${revision.isActive ? '（当前激活）' : ''}`"
              :value="revision.id"
            />
          </ElSelect>
        </ElFormItem>
        <div class="backtest-form__times">
          <ElFormItem label="开始时间（UTC+8）">
            <ElDatePicker
              v-model="backtestForm.startTime"
              type="datetime"
              class="backtest-form__full"
            />
          </ElFormItem>
          <ElFormItem label="结束时间（UTC+8）">
            <ElDatePicker
              v-model="backtestForm.endTime"
              type="datetime"
              class="backtest-form__full"
            />
          </ElFormItem>
        </div>
        <div class="backtest-form__numbers">
          <ElFormItem label="初始资金"
            ><ElInput v-model="backtestForm.initialCapital"
          /></ElFormItem>
          <ElFormItem label="手续费率"><ElInput v-model="backtestForm.feeRate" /></ElFormItem>
          <ElFormItem label="滑点率"><ElInput v-model="backtestForm.slippageRate" /></ElFormItem>
        </div>
      </ElForm>
      <template #footer>
        <ElButton @click="backtestVisible = false">取消</ElButton>
        <ElButton type="primary" :loading="Boolean(runningId)" @click="submitBacktest"
          >开始回测</ElButton
        >
      </template>
    </ElDialog>
  </div>
</template>

<script setup lang="ts">
  import {
    ArrowDown,
    Clock,
    Collection,
    Delete,
    Edit,
    Folder,
    FolderOpened,
    MoreFilled,
    Plus,
    Rank,
    SwitchButton,
    VideoPlay
  } from '@element-plus/icons-vue'
  import { ElMessage, ElMessageBox } from 'element-plus'
  import { VueDraggable } from 'vue-draggable-plus'
  import { useAuth } from '@/hooks/core/useAuth'
  import {
    fetchActivateWorkflowDefinition,
    fetchDeactivateWorkflowDefinition,
    fetchDeleteWorkflowDefinition,
    fetchRunWorkflowDefinition,
    fetchWorkflowDefinitionList,
    type WorkflowDefinitionItem,
    type WorkflowDefinitionVersionItem
  } from '@/api/scheduler'
  import {
    assignWorkflowGroup,
    createWorkflowGroup,
    createWorkflow,
    deleteWorkflowGroup,
    fetchWorkflowGroups,
    fetchWorkflowRuns,
    fetchWorkflowTemplates,
    updateWorkflowGroup,
    updateWorkflowGroupOrder,
    type WorkflowGroup,
    type WorkflowStatus,
    type WorkflowTemplate
  } from '@/api/workflows'
  import { formatDateTime } from '@/utils/date'

  defineOptions({ name: 'SchedulerWorkflowDefinitionsPage' })

  const router = useRouter()
  const { hasAuth } = useAuth()
  const loading = ref(false)
  const actingId = ref<number>()
  const runningId = ref<number>()
  const creating = ref(false)
  const createVisible = ref(false)
  const workflowTemplates = ref<WorkflowTemplate[]>([])
  const createForm = reactive({ name: '', templateKey: 'blank', groupId: 0 })
  const selectedTemplate = computed(() =>
    workflowTemplates.value.find((item) => item.key === createForm.templateKey)
  )
  const definitions = ref<WorkflowDefinitionItem[]>([])
  const workflowGroups = ref<WorkflowGroup[]>([])
  const selectedGroup = ref<'all' | 'ungrouped' | number>('all')
  const selectedDefinitions = ref<WorkflowDefinitionItem[]>([])
  const tableRef = ref<{ elTableRef?: { clearSelection: () => void } }>()
  const assigning = ref(false)
  const versionDialogVisible = ref(false)
  const versionDetail = ref<WorkflowDefinitionItem | null>(null)
  const deletingVersionId = ref<number>()
  const versionRows = computed(() => versionDetail.value?.versions || [])
  const utc8OffsetMs = 8 * 60 * 60 * 1000
  const utc8PickerDate = (timestamp = Date.now()) => {
    const shifted = new Date(timestamp + utc8OffsetMs)
    return new Date(
      shifted.getUTCFullYear(),
      shifted.getUTCMonth(),
      shifted.getUTCDate(),
      shifted.getUTCHours(),
      shifted.getUTCMinutes(),
      shifted.getUTCSeconds()
    )
  }
  const utc8PickerISOString = (value: Date) =>
    new Date(
      Date.UTC(
        value.getFullYear(),
        value.getMonth(),
        value.getDate(),
        value.getHours(),
        value.getMinutes(),
        value.getSeconds(),
        value.getMilliseconds()
      ) - utc8OffsetMs
    ).toISOString()
  const backtestVisible = ref(false)
  const backtestWorkflow = ref<WorkflowDefinitionItem | null>(null)
  const backtestError = ref('')
  const backtestForm = reactive({
    definitionId: 0,
    startTime: utc8PickerDate(Date.now() - 30 * 24 * 60 * 60 * 1000),
    endTime: utc8PickerDate(),
    initialCapital: '10000',
    feeRate: '0.001',
    slippageRate: '0.0005'
  })
  const initialFilters = { keyword: '', status: '' }
  const formFilters = reactive({ ...initialFilters })
  const appliedFilters = reactive({ ...initialFilters })

  const formItems = computed(() => [
    {
      label: '名称',
      key: 'keyword',
      type: 'input',
      props: { clearable: true, placeholder: '搜索工作流名称' }
    },
    {
      label: '状态',
      key: 'status',
      type: 'select',
      props: {
        clearable: true,
        options: [
          { label: '已激活', value: 'active' },
          { label: '未激活', value: 'inactive' },
          { label: '异常', value: 'error' }
        ]
      }
    }
  ])

  const ungroupedCount = computed(
    () => definitions.value.filter((item) => item.groupId === null).length
  )

  const groupCount = (groupId: number) =>
    definitions.value.filter((item) => item.groupId === groupId).length

  const groupName = (groupId: number | null) =>
    groupId === null
      ? '未分组'
      : workflowGroups.value.find((group) => group.id === groupId)?.name || '未分组'

  const filteredDefinitions = computed(() => {
    const keyword = appliedFilters.keyword.trim().toLowerCase()
    return definitions.value.filter(
      (item) =>
        (selectedGroup.value === 'all' ||
          (selectedGroup.value === 'ungrouped'
            ? item.groupId === null
            : item.groupId === selectedGroup.value)) &&
        (!keyword || item.displayName.toLowerCase().includes(keyword)) &&
        (!appliedFilters.status || item.workflowStatus === appliedFilters.status)
    )
  })

  const clearSelection = () => {
    selectedDefinitions.value = []
    tableRef.value?.elTableRef?.clearSelection()
  }

  const selectGroup = (group: 'all' | 'ungrouped' | number) => {
    selectedGroup.value = group
    clearSelection()
  }

  const statusLabel = (status: WorkflowStatus) =>
    ({ inactive: '未激活', active: '已激活', error: '异常' })[status]

  const statusTagType = (status: WorkflowStatus) => {
    if (status === 'active') return 'success'
    if (status === 'error') return 'danger'
    return 'info'
  }

  const lifecycleLabel = (status: WorkflowStatus) =>
    status === 'inactive' ? '激活' : status === 'error' ? '恢复为未激活' : '停用'

  const loadPageData = async () => {
    loading.value = true
    try {
      const [groupResult, items] = await Promise.all([
        fetchWorkflowGroups(),
        fetchWorkflowDefinitionList()
      ])
      workflowGroups.value = groupResult.items
      definitions.value = items
      if (
        typeof selectedGroup.value === 'number' &&
        !workflowGroups.value.some((group) => group.id === selectedGroup.value)
      ) {
        selectedGroup.value = 'all'
      }
      clearSelection()
    } finally {
      loading.value = false
    }
  }

  const promptGroupName = async (title: string, initialValue = '') => {
    try {
      const { value } = await ElMessageBox.prompt('', title, {
        inputValue: initialValue,
        inputPlaceholder: '请输入分组名称',
        inputValidator: (input) => {
          const length = [...String(input || '').trim()].length
          return (length > 0 && length <= 80) || '分组名称须为 1 至 80 个字符'
        },
        confirmButtonText: '保存',
        cancelButtonText: '取消'
      })
      return String(value || '').trim()
    } catch {
      return ''
    }
  }

  const createGroup = async () => {
    const name = await promptGroupName('新建分组')
    if (!name) return
    const group = await createWorkflowGroup(name)
    workflowGroups.value.push(group)
    selectGroup(group.id)
    ElMessage.success('分组已创建')
  }

  const renameGroup = async (group: WorkflowGroup) => {
    const name = await promptGroupName('重命名分组', group.name)
    if (!name || name === group.name) return
    const updated = await updateWorkflowGroup(group.id, name)
    const index = workflowGroups.value.findIndex((item) => item.id === group.id)
    if (index >= 0) workflowGroups.value[index] = updated
    ElMessage.success('分组已重命名')
  }

  const deleteGroup = async (group: WorkflowGroup) => {
    const count = groupCount(group.id)
    try {
      await ElMessageBox.confirm(
        count
          ? `删除分组“${group.name}”？其中 ${count} 个工作流将移至“未分组”。`
          : `删除空分组“${group.name}”？`,
        '删除分组',
        { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' }
      )
    } catch {
      return
    }
    await deleteWorkflowGroup(group.id)
    definitions.value.forEach((item) => {
      if (item.groupId === group.id) item.groupId = null
    })
    workflowGroups.value = workflowGroups.value.filter((item) => item.id !== group.id)
    if (selectedGroup.value === group.id) selectGroup('ungrouped')
    ElMessage.success('分组已删除')
  }

  const persistGroupOrder = async () => {
    try {
      const result = await updateWorkflowGroupOrder(workflowGroups.value.map((group) => group.id))
      workflowGroups.value = result.items
    } catch {
      workflowGroups.value = (await fetchWorkflowGroups()).items
      ElMessage.error('保存分组顺序失败，已恢复服务器顺序')
    }
  }

  const moveGroup = async (index: number, offset: -1 | 1) => {
    const target = index + offset
    if (target < 0 || target >= workflowGroups.value.length) return
    const [group] = workflowGroups.value.splice(index, 1)
    workflowGroups.value.splice(target, 0, group)
    await persistGroupOrder()
  }

  const handleGroupCommand = async (
    command: string | number | Record<string, unknown>,
    group: WorkflowGroup,
    index: number
  ) => {
    if (command === 'rename') await renameGroup(group)
    if (command === 'up') await moveGroup(index, -1)
    if (command === 'down') await moveGroup(index, 1)
    if (command === 'delete') await deleteGroup(group)
  }

  const handleSelectionChange = (rows: WorkflowDefinitionItem[]) => {
    selectedDefinitions.value = rows
  }

  const assignDefinitions = async (rows: WorkflowDefinitionItem[], groupId: number | null) => {
    if (!rows.length || assigning.value) return
    assigning.value = true
    try {
      await assignWorkflowGroup(
        rows.map((row) => Number(row.code)),
        groupId
      )
      const rowIDs = new Set(rows.map((row) => row.id))
      definitions.value.forEach((item) => {
        if (rowIDs.has(item.id)) item.groupId = groupId
      })
      clearSelection()
      ElMessage.success(rows.length === 1 ? '工作流已移动' : `已移动 ${rows.length} 个工作流`)
    } finally {
      assigning.value = false
    }
  }

  const assignSelectedToGroup = async (command: number | string | Record<string, unknown>) => {
    await assignDefinitions(selectedDefinitions.value, Number(command) || null)
  }

  const openLogs = async (row: WorkflowDefinitionItem) => {
    try {
      const result = await fetchWorkflowRuns(row.id, { limit: 1 })
      const run = result.records[0]
      if (!run) {
        ElMessage.info('该工作流暂无运行记录')
        return
      }
      await router.push({
        path: `/scheduler/execution/${run.id}/detail`,
        query: {
          workflowId: row.code,
          workflowName: row.displayName,
          ...(run.triggerType === 'stream' ? { followLatest: '1' } : {})
        }
      })
    } catch (error: any) {
      ElMessage.error(error?.message || '加载工作流运行日志失败')
    }
  }

  const openVersionDialog = (row: WorkflowDefinitionItem) => {
    versionDetail.value = row
    versionDialogVisible.value = true
  }

  const openVersionEditor = async (row: WorkflowDefinitionVersionItem) => {
    versionDialogVisible.value = false
    await router.push(`/scheduler/workflow/${row.id}/edit`)
  }

  const deleteVersion = async (row: WorkflowDefinitionVersionItem) => {
    try {
      await ElMessageBox.confirm(
        `删除历史版本 v${row.version}？版本配置及密钥将永久删除。已有执行记录的版本不能删除。`,
        '删除版本',
        { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' }
      )
    } catch {
      return
    }
    deletingVersionId.value = row.id
    try {
      await fetchDeleteWorkflowDefinition(row.id)
      if (versionDetail.value?.versions) {
        versionDetail.value.versions = versionDetail.value.versions.filter(
          (version) => version.id !== row.id
        )
      }
      ElMessage.success(`历史版本 v${row.version} 已删除`)
    } finally {
      deletingVersionId.value = undefined
    }
  }

  const toggleLifecycle = async (row: WorkflowDefinitionItem) => {
    const activate = row.workflowStatus === 'inactive'
    await ElMessageBox.confirm(
      `${activate ? '激活' : '停用'}工作流“${row.displayName}”？`,
      lifecycleLabel(row.workflowStatus),
      { type: activate ? 'info' : 'warning' }
    )
    actingId.value = row.id
    try {
      if (activate) await fetchActivateWorkflowDefinition(row.id)
      else await fetchDeactivateWorkflowDefinition(row.id)
      await loadPageData()
    } finally {
      actingId.value = undefined
    }
  }

  const isBacktestWorkflow = (row: WorkflowDefinitionItem) =>
    row.graph.schemaVersion === 2 && Boolean(row.graph.entryPoints?.backtest)

  const openCreateWorkflow = async () => {
    if (!workflowTemplates.value.length) {
      workflowTemplates.value = (await fetchWorkflowTemplates()).items
    }
    createForm.name = ''
    createForm.templateKey = 'blank'
    createForm.groupId = typeof selectedGroup.value === 'number' ? selectedGroup.value : 0
    createVisible.value = true
  }

  const submitCreateWorkflow = async () => {
    const name = createForm.name.trim()
    if (!name) {
      ElMessage.warning('请输入工作流名称')
      return
    }
    creating.value = true
    try {
      const workflow = await createWorkflow({
        name,
        description: '',
        templateKey: createForm.templateKey as Parameters<typeof createWorkflow>[0]['templateKey'],
        groupId: createForm.groupId || null
      })
      createVisible.value = false
      await router.push(`/scheduler/workflow/${workflow.id}/edit`)
    } finally {
      creating.value = false
    }
  }

  const runWorkflow = async (row: WorkflowDefinitionItem) => {
    if (isBacktestWorkflow(row)) {
      backtestWorkflow.value = row
      backtestError.value = ''
      backtestForm.definitionId =
        row.versions?.find((revision) => revision.isActive)?.id || row.versions?.[0]?.id || row.id
      backtestForm.endTime = utc8PickerDate()
      backtestForm.startTime = utc8PickerDate(Date.now() - 30 * 24 * 60 * 60 * 1000)
      backtestVisible.value = true
      return
    }
    runningId.value = row.id
    try {
      const result = await fetchRunWorkflowDefinition(row.id, { startEntryKeys: [] })
      const run = result.executions[0]
      ElMessage.success('运行已加入队列')
      if (run) {
        await router.push({
          path: `/scheduler/execution/${run.id}/detail`,
          query: { workflowId: row.code, workflowName: row.displayName }
        })
      }
    } finally {
      runningId.value = undefined
    }
  }

  const submitBacktest = async () => {
    const row = backtestWorkflow.value
    backtestError.value = ''
    if (!row || !backtestForm.definitionId || !backtestForm.startTime || !backtestForm.endTime) {
      backtestError.value = '请选择策略版本和回测时间'
      return
    }
    if (backtestForm.startTime >= backtestForm.endTime) {
      backtestError.value = '开始时间必须早于结束时间'
      return
    }
    runningId.value = row.id
    let result
    try {
      result = await fetchRunWorkflowDefinition(backtestForm.definitionId, {
        startEntryKeys: ['backtest'],
        entryPoint: 'backtest',
        inputs: {
          startTime: utc8PickerISOString(backtestForm.startTime),
          endTime: utc8PickerISOString(backtestForm.endTime),
          initialCapital: backtestForm.initialCapital,
          feeRate: backtestForm.feeRate,
          slippageRate: backtestForm.slippageRate
        }
      })
    } catch {
      backtestError.value = '回测启动失败，请检查所选策略版本和工作流配置'
      return
    } finally {
      runningId.value = undefined
    }
    backtestVisible.value = false
    const run = result.executions[0]
    ElMessage.success('回测已加入队列')
    if (run) {
      await router.push({
        path: `/scheduler/execution/${run.id}/detail`,
        query: { workflowId: row.code, workflowName: row.displayName }
      })
    }
  }

  const handleSearch = () => {
    Object.assign(appliedFilters, formFilters)
    clearSelection()
  }
  const handleReset = () => {
    Object.assign(formFilters, initialFilters)
    Object.assign(appliedFilters, initialFilters)
    clearSelection()
  }

  onMounted(loadPageData)
</script>

<style scoped lang="scss">
  .workflow-library {
    display: grid;
    grid-template-columns: 220px minmax(0, 1fr);
    min-height: 420px;
  }

  .workflow-groups {
    min-width: 0;
    padding-right: 16px;
    border-right: 1px solid var(--el-border-color-lighter);
  }

  .workflow-groups__header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    min-height: 40px;
    margin-bottom: 8px;
    font-size: 14px;
    font-weight: 600;
    color: var(--el-text-color-primary);
  }

  .workflow-groups__scroll {
    max-height: calc(100vh - 300px);
    overflow-y: auto;
  }

  .workflow-groups__fixed,
  .workflow-groups__custom {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .workflow-groups__custom {
    margin-top: 4px;
  }

  .workflow-group {
    display: flex;
    gap: 2px;
    align-items: center;
    min-width: 0;
  }

  .workflow-group-filter {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr) auto;
    gap: 8px;
    align-items: center;
    width: 100%;
    min-height: 36px;
    padding: 0 10px;
    font: inherit;
    color: var(--el-text-color-regular);
    text-align: left;
    letter-spacing: 0;
    cursor: pointer;
    background: transparent;
    border: 0;
    border-radius: 4px;
  }

  .workflow-group-filter:hover {
    background: var(--el-fill-color-light);
  }

  .workflow-group-filter.is-active {
    color: var(--el-color-primary);
    background: var(--el-color-primary-light-9);
  }

  .workflow-group-filter--custom {
    flex: 1;
    min-width: 0;
  }

  .workflow-group-filter__drag {
    flex: 0 0 28px;
    width: 28px;
    height: 28px;
    cursor: move;
  }

  .workflow-group-filter__name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .workflow-group-filter__count {
    min-width: 20px;
    font-size: 12px;
    color: var(--el-text-color-secondary);
    text-align: right;
  }

  .workflow-table {
    min-width: 0;
    padding-left: 16px;
  }

  .version-header {
    display: flex;
    gap: 16px;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 16px;
  }

  .version-header__title {
    font-size: 16px;
    font-weight: 600;
    color: var(--el-text-color-primary);
  }

  .version-header__description {
    margin-top: 6px;
    font-size: 12px;
    color: var(--el-text-color-secondary);
  }

  .backtest-form__full {
    width: 100%;
  }

  .backtest-form__error {
    margin-bottom: 16px;
  }

  .create-template-description {
    margin-top: -6px;
    color: var(--el-text-color-secondary);
  }

  .backtest-form__times,
  .backtest-form__numbers {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px;
  }

  .backtest-form__numbers {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }

  @media (width <= 640px) {
    .backtest-form__times,
    .backtest-form__numbers {
      grid-template-columns: 1fr;
    }
  }

  @media (width <= 900px) {
    .workflow-library {
      display: block;
    }

    .workflow-groups {
      padding-right: 0;
      padding-bottom: 12px;
      margin-bottom: 8px;
      border-right: 0;
      border-bottom: 1px solid var(--el-border-color-lighter);
    }

    .workflow-groups__scroll {
      display: flex;
      gap: 4px;
      max-height: none;
      padding-bottom: 4px;
      overflow-x: auto;
      overflow-y: hidden;
    }

    .workflow-groups__fixed,
    .workflow-groups__custom {
      flex-direction: row;
      flex-shrink: 0;
      margin-top: 0;
    }

    .workflow-group {
      flex: 0 0 auto;
      min-width: 160px;
    }

    .workflow-group-filter {
      min-width: 128px;
    }

    .workflow-table {
      padding-left: 0;
    }
  }
</style>
