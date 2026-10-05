<template>
  <main class="workflow-library">
    <header class="library-header"
      ><div><h1>工作流</h1><p>编辑草稿，发布后启用自动运行</p></div
      ><ElSpace
        ><ElButton @click="load">刷新</ElButton
        ><ElButton v-if="hasAuth('workflow_groups.manage')" @click="groupsVisible = true"
          >管理分组</ElButton
        ><ElButton v-if="hasAuth('workflows.create')" type="primary" @click="openCreate"
          >创建工作流</ElButton
        ></ElSpace
      ></header
    >
    <ElForm inline @submit.prevent="search"
      ><ElFormItem label="名称"
        ><ElInput v-model="filters.keyword" clearable @keyup.enter="search" /></ElFormItem
      ><ElFormItem label="状态"
        ><ElSelect v-model="filters.status" clearable style="width: 150px"
          ><ElOption label="未启用" value="inactive" /><ElOption
            label="已启用"
            value="active" /><ElOption label="异常" value="error" /></ElSelect></ElFormItem
      ><ElFormItem label="分组"
        ><ElSelect v-model="filters.groupId" clearable style="width: 170px"
          ><ElOption label="未分组" :value="0" /><ElOption
            v-for="group in groups"
            :key="group.id"
            :label="group.name"
            :value="group.id" /></ElSelect></ElFormItem
      ><ElButton @click="search">查询</ElButton></ElForm
    >
    <ElAlert v-if="error" type="error" :title="error" :closable="false" />
    <ElTable
      v-loading="loading"
      :data="page.records"
      row-key="id"
      empty-text="暂无工作流，创建一个工作流开始使用"
    >
      <ElTableColumn label="工作流" min-width="220"
        ><template #default="{ row }"
          ><ElButton
            link
            type="primary"
            @click="router.push(`/scheduler/workflow/${row.id}/edit`)"
            >{{ row.name }}</ElButton
          ><p class="row-description">{{ row.description }}</p></template
        ></ElTableColumn
      >
      <ElTableColumn label="状态" width="110"
        ><template #default="{ row }"
          ><ElTag
            :type="row.status === 'active' ? 'success' : row.status === 'error' ? 'danger' : 'info'"
            >{{ statusLabels[row.status] }}</ElTag
          ></template
        ></ElTableColumn
      >
      <ElTableColumn label="版本" min-width="180"
        ><template #default="{ row }"
          >草稿 #{{ row.draftRevisionId }}<br />已发布
          {{ row.publishedRevisionId ? '#' + row.publishedRevisionId : '暂无' }}</template
        ></ElTableColumn
      >
      <ElTableColumn label="最近运行" min-width="120"
        ><template #default="{ row }"
          ><ElButton
            v-if="row.latestRunId"
            link
            @click="router.push(`/scheduler/execution/${row.latestRunId}/detail`)"
            >{{ runStatusLabel(row.latestRunStatus) }}</ElButton
          ><span v-else>暂无运行</span></template
        ></ElTableColumn
      >
      <ElTableColumn label="分组" min-width="140"
        ><template #default="{ row }"
          ><ElSelect
            v-if="hasAuth('workflows.update')"
            :model-value="row.groupId || 0"
            @update:model-value="move(row, $event)"
            ><ElOption label="未分组" :value="0" /><ElOption
              v-for="group in groups"
              :key="group.id"
              :label="group.name"
              :value="group.id" /></ElSelect
          ><span v-else>{{
            groups.find((group) => group.id === row.groupId)?.name || '未分组'
          }}</span></template
        ></ElTableColumn
      >
      <ElTableColumn label="操作" min-width="280"
        ><template #default="{ row }"
          ><ElSpace wrap
            ><ElButton
              v-if="hasAuth('workflows.activate')"
              link
              :disabled="!row.publishedRevisionId"
              @click="lifecycle(row)"
              >{{ row.status === 'active' ? '停用' : '启用' }}</ElButton
            ><ElButton
              link
              @click="router.push({ path: '/scheduler/execution', query: { workflowId: row.id } })"
              >历史运行</ElButton
            ><ElButton v-if="hasAuth('workflows.share')" link @click="openGrants(row)"
              >授权</ElButton
            ><ElButton v-if="hasAuth('workflows.delete')" link type="danger" @click="remove(row)"
              >删除</ElButton
            ></ElSpace
          ></template
        ></ElTableColumn
      >
    </ElTable>
    <footer class="page-footer"
      ><span>共 {{ page.total }} 个工作流</span
      ><ElSpace
        ><ElButton :disabled="!cursors.length" @click="previous">上一页</ElButton
        ><ElButton :disabled="!page.hasMore" @click="next">下一页</ElButton></ElSpace
      ></footer
    >
    <ElDialog v-model="createVisible" title="创建工作流" width="min(500px, 94vw)"
      ><ElForm label-position="top"
        ><ElFormItem label="名称"><ElInput v-model="createForm.name" maxlength="120" /></ElFormItem
        ><ElFormItem label="模板"
          ><ElSelect v-model="createForm.templateKey"
            ><ElOption
              v-for="template in templates"
              :key="template.key"
              :label="template.name"
              :value="template.key" /></ElSelect
          ><p>{{
            templates.find((item) => item.key === createForm.templateKey)?.description
          }}</p></ElFormItem
        ></ElForm
      ><template #footer
        ><ElButton @click="createVisible = false">取消</ElButton
        ><ElButton type="primary" :loading="creating" @click="create"
          >创建并编辑</ElButton
        ></template
      ></ElDialog
    >
    <ElDialog
      v-model="grantsVisible"
      :title="`授权 · ${grantWorkflow?.name || ''}`"
      width="min(780px, 94vw)"
      ><p>授权只在接收者同时拥有对应角色能力时生效。</p
      ><div v-for="(grant, index) in grants" :key="index" class="grant-row"
        ><ElSelect
          :model-value="grant.userId ? 'user' : 'role'"
          @update:model-value="
            grant.userId = $event === 'user' ? 1 : undefined
            grant.roleId = $event === 'role' ? 1 : undefined
          "
          ><ElOption label="用户 ID" value="user" /><ElOption
            label="角色 ID"
            value="role" /></ElSelect
        ><ElInputNumber v-if="grant.userId" v-model="grant.userId" :min="1" /><ElInputNumber
          v-else
          v-model="grant.roleId"
          :min="1"
        /><ElSelect v-model="grant.permissions" multiple placeholder="允许操作"
          ><ElOption
            v-for="permission in workflowPermissions"
            :key="permission.code"
            :label="permission.title"
            :value="permission.code"
            :disabled="!hasAuth(permission.code)" /></ElSelect
        ><ElButton type="danger" link @click="grants.splice(index, 1)">删除</ElButton></div
      ><ElButton @click="grants.push({ userId: 1, permissions: ['workflows.read'] })"
        >添加授权</ElButton
      ><template #footer
        ><ElButton @click="grantsVisible = false">取消</ElButton
        ><ElButton type="primary" @click="saveGrants">保存授权</ElButton></template
      ></ElDialog
    >
    <ElDialog v-model="groupsVisible" title="管理分组" width="min(560px, 94vw)"
      ><div v-for="(group, index) in groups" :key="group.id" class="group-row"
        ><span>{{ group.name }}</span
        ><ElButton @click="renameGroup(group)">重命名</ElButton
        ><ElButton :disabled="index === 0" @click="reorderGroup(index)">上移</ElButton
        ><ElButton type="danger" @click="removeGroup(group)">删除</ElButton></div
      ><ElButton @click="addGroup">新建分组</ElButton></ElDialog
    >
  </main>
</template>
<script setup lang="ts">
  import { ElMessage, ElMessageBox } from 'element-plus'
  import { useAuth } from '@/hooks/core/useAuth'
  import {
    fetchWorkflows,
    fetchWorkflowGroups,
    fetchWorkflowTemplates,
    createWorkflow,
    deleteWorkflow,
    applyWorkflowLifecycle,
    assignWorkflowGroup,
    fetchWorkflowGrants,
    replaceWorkflowGrants,
    createWorkflowGroup,
    updateWorkflowGroup,
    deleteWorkflowGroup,
    updateWorkflowGroupOrder,
    type WorkflowSummary,
    type WorkflowTemplate,
    type WorkflowGroup,
    type WorkflowGrant
  } from '@/api/workflows'
  import { runStatusLabel } from '@/components/workflow/status'
  const router = useRouter(),
    { hasAuth } = useAuth()
  const loading = ref(false),
    creating = ref(false),
    error = ref(''),
    groups = ref<WorkflowGroup[]>([]),
    templates = ref<WorkflowTemplate[]>([])
  const page = ref<Api.Common.PaginatedResponse<WorkflowSummary>>({
    records: [],
    total: 0,
    hasMore: false,
    nextCursor: ''
  })
  const filters = reactive({ keyword: '', status: '', groupId: undefined as number | undefined }),
    applied = reactive({ ...filters })
  const cursor = ref(''),
    cursors = ref<string[]>([])
  const statusLabels: Record<string, string> = {
    inactive: '未启用',
    active: '已启用',
    error: '异常'
  }
  const load = async () => {
    loading.value = true
    error.value = ''
    try {
      page.value = await fetchWorkflows({ ...applied, cursor: cursor.value, limit: 20 })
    } catch (cause: any) {
      error.value = cause.message
    } finally {
      loading.value = false
    }
  }
  const search = () => {
    Object.assign(applied, filters)
    cursor.value = ''
    cursors.value = []
    void load()
  }
  const next = () => {
    cursors.value.push(cursor.value)
    cursor.value = page.value.nextCursor
    void load()
  }
  const previous = () => {
    cursor.value = cursors.value.pop() || ''
    void load()
  }
  const move = async (row: WorkflowSummary, id: number) => {
    await assignWorkflowGroup([row.id], id || null)
    await load()
  }
  const lifecycle = async (row: WorkflowSummary) => {
    const action = row.status === 'active' ? 'deactivate' : 'activate'
    await ElMessageBox.confirm(
      `${action === 'activate' ? '启用' : '停用'}“${row.name}”？`,
      '工作流状态'
    )
    await applyWorkflowLifecycle(row.id, action)
    await load()
  }
  const remove = async (row: WorkflowSummary) => {
    await ElMessageBox.confirm(`删除“${row.name}”及其版本和运行记录？`, '删除工作流', {
      type: 'warning'
    })
    await deleteWorkflow(row.id)
    await load()
  }
  const createVisible = ref(false),
    createForm = reactive({ name: '', templateKey: 'blank' })
  const openCreate = async () => {
    templates.value = (await fetchWorkflowTemplates()).items
    createVisible.value = true
  }
  const create = async () => {
    if (!createForm.name.trim()) {
      ElMessage.warning('请输入名称')
      return
    }
    creating.value = true
    try {
      const row = await createWorkflow({
        ...createForm,
        description: '',
        groupId: applied.groupId || null
      })
      createVisible.value = false
      await router.push(`/scheduler/workflow/${row.id}/edit`)
    } finally {
      creating.value = false
    }
  }
  const grantsVisible = ref(false),
    grantWorkflow = ref<WorkflowSummary>(),
    grants = ref<WorkflowGrant[]>([])
  const workflowPermissions = [
    { code: 'workflows.read', title: '查看' },
    { code: 'workflows.update', title: '编辑' },
    { code: 'workflows.publish', title: '发布' },
    { code: 'workflows.activate', title: '启停' },
    { code: 'workflows.run', title: '运行' },
    { code: 'workflows.cancel', title: '取消运行' },
    { code: 'workflows.retry', title: '重试运行' },
    { code: 'workflows.delete', title: '删除' },
    { code: 'workflows.share', title: '授权' },
    { code: 'workflows.secrets.manage', title: '凭据' },
    { code: 'human_tasks.read', title: '查看待办' },
    { code: 'human_tasks.decide', title: '处理待办' }
  ]
  const openGrants = async (row: WorkflowSummary) => {
    grantWorkflow.value = row
    grants.value = (await fetchWorkflowGrants(row.id)).items
    grantsVisible.value = true
  }
  const saveGrants = async () => {
    if (!grantWorkflow.value) return
    await replaceWorkflowGrants(grantWorkflow.value.id, grants.value)
    grantsVisible.value = false
    ElMessage.success('授权已保存')
  }
  const groupsVisible = ref(false)
  const namePrompt = async (title: string, value = '') => {
    const result = await ElMessageBox.prompt('名称为 1 至 80 个字符', title, {
      inputValue: value,
      inputValidator: (value) => Boolean(value.trim()) && [...value.trim()].length <= 80
    })
    return result.value.trim()
  }
  const refreshGroups = async () => {
    groups.value = (await fetchWorkflowGroups()).items
  }
  const addGroup = async () => {
    await createWorkflowGroup(await namePrompt('新建分组'))
    await refreshGroups()
  }
  const renameGroup = async (group: WorkflowGroup) => {
    await updateWorkflowGroup(group.id, await namePrompt('重命名分组', group.name))
    await refreshGroups()
  }
  const removeGroup = async (group: WorkflowGroup) => {
    await ElMessageBox.confirm('删除分组后工作流移至未分组', '删除分组')
    await deleteWorkflowGroup(group.id)
    await refreshGroups()
    search()
  }
  const reorderGroup = async (index: number) => {
    const ids = groups.value.map((group) => group.id)
    ;[ids[index - 1], ids[index]] = [ids[index], ids[index - 1]]
    groups.value = (await updateWorkflowGroupOrder(ids)).items
  }
  onMounted(async () => {
    await Promise.all([refreshGroups(), load()])
  })
</script>
<style scoped>
  .workflow-library {
    padding: 24px;
    background: var(--el-bg-color);
    color: var(--el-text-color-primary);
  }
  .library-header {
    display: flex;
    justify-content: space-between;
    flex-wrap: wrap;
    align-items: center;
    gap: 16px;
    margin-bottom: 28px;
  }
  h1 {
    margin: 0 0 8px;
    font-size: 26px;
    font-weight: 600;
  }
  p {
    color: var(--el-text-color-secondary);
  }
  .row-description {
    margin: 4px 0;
    font-size: 12px;
  }
  .page-footer {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding-top: 20px;
  }
  .grant-row {
    display: grid;
    grid-template-columns: 110px 140px minmax(0, 1fr) auto;
    gap: 10px;
    margin-bottom: 16px;
  }
  .group-row {
    display: flex;
    gap: 8px;
    align-items: center;
    margin-bottom: 12px;
  }
  .group-row span {
    flex: 1;
  }
  @media (max-width: 760px) {
    .workflow-library {
      padding: 12px;
    }
    .grant-row {
      grid-template-columns: 1fr;
    }
  }
</style>
