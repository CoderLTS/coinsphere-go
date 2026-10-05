<template>
  <main class="results-page">
    <header
      ><div><h1>共享结果</h1><p>查看已授权的固定范围结果</p></div
      ><ElSpace
        ><ElButton @click="load">刷新</ElButton
        ><ElButton v-if="hasAuth('result_views.manage')" type="primary" @click="openCreate"
          >创建结果视图</ElButton
        ></ElSpace
      ></header
    >
    <ElAlert v-if="error" :title="error" type="error" :closable="false" />
    <div v-loading="loading" class="results-layout"
      ><aside aria-label="结果视图"
        ><button
          v-for="view in views"
          :key="view.id"
          :class="{ selected: selected?.id === view.id }"
          @click="select(view)"
          ><strong>{{ view.name }}</strong
          ><span>{{
            view.status === 'active'
              ? pageTitle(view)
              : view.status === 'inactive'
                ? '未开放'
                : '已撤销'
          }}</span></button
        ><ElEmpty v-if="!views.length" description="暂无获授权的结果视图" /></aside
      ><section
        ><div v-if="selected" class="view-toolbar"
          ><h2>{{ selected.name }}</h2
          ><ElSpace v-if="selected.canManage && selected.status !== 'revoked'"
            ><ElButton @click="openGrants(selected)">管理授权</ElButton
            ><ElButton @click="changeStatus(selected)">{{
              selected.status === 'active' ? '暂停开放' : '开放视图'
            }}</ElButton
            ><ElButton type="danger" plain @click="revoke(selected)">撤销视图</ElButton></ElSpace
          ></div
        ><component
          :is="resultComponent"
          v-if="selected?.status === 'active' && resultComponent"
          :key="selected.id"
          :view="selected"
          :context="{
            viewId: selected.id,
            pluginId: selected.pluginId,
            pageKey: selected.pageKey,
            allowedActions: selected.allowedActions
          }" /><ElEmpty v-else description="选择一个可用结果视图" /></section
    ></div>
    <ElDialog v-model="createVisible" title="创建结果视图" width="min(720px, 95vw)" destroy-on-close
      ><ElForm label-position="top"
        ><ElFormItem label="名称"><ElInput v-model="form.name" maxlength="120" /></ElFormItem
        ><ElFormItem label="结果页面"
          ><ElSelect v-model="pageID" @change="changePage"
            ><ElOption
              v-for="page in pageChoices"
              :key="page.id"
              :value="page.id"
              :label="page.title" /></ElSelect></ElFormItem
        ><component
          :is="configComponent"
          v-if="configComponent && selectedPage"
          v-model:scope="form.scope"
          v-model:filters="form.filters"
          :scope-schema="selectedPage.scopeSchema"
          :filter-schema="selectedPage.filterSchema" /><template v-else-if="selectedPage"
          ><h3>固定范围</h3
          ><WorkflowSchemaFields
            :schema="selectedPage.scopeSchema"
            :config="form.scope"
            @update="(key, value) => (form.scope[key] = value)" /><h3>筛选条件</h3
          ><WorkflowSchemaFields
            :schema="selectedPage.filterSchema"
            :config="form.filters"
            @update="(key, value) => setFilter(key, value)" /></template
        ><ElFormItem label="允许操作"
          ><ElCheckboxGroup v-model="form.allowedActions"
            ><ElCheckbox
              v-for="action in selectedPage?.actions || []"
              :key="action"
              :value="action"
              :disabled="!hasAuth(selectedPage?.actionPermissions[action] || '')"
              >{{ action }}</ElCheckbox
            ></ElCheckboxGroup
          ></ElFormItem
        ><ElFormItem label="用户 ID"
          ><ElSelect
            v-model="recipientIDs"
            multiple
            filterable
            allow-create
            placeholder="输入用户 ID 后回车" /></ElFormItem
        ><ElFormItem label="角色代码"
          ><ElSelect
            v-model="form.roleCodes"
            multiple
            filterable
            allow-create
            placeholder="输入角色代码后回车" /></ElFormItem></ElForm
      ><template #footer
        ><ElButton @click="createVisible = false">取消</ElButton
        ><ElButton
          type="primary"
          :loading="saving"
          :disabled="!selectedPage || Boolean(error)"
          @click="create"
          >创建结果视图</ElButton
        ></template
      ></ElDialog
    >
    <ElDialog v-model="grantsVisible" title="管理结果授权" width="min(520px, 95vw)"
      ><ElForm label-position="top"
        ><ElFormItem label="用户 ID"
          ><ElSelect v-model="grantIDs" multiple filterable allow-create /></ElFormItem
        ><ElFormItem label="角色代码"
          ><ElSelect v-model="grantRoles" multiple filterable allow-create /></ElFormItem></ElForm
      ><template #footer
        ><ElButton @click="grantsVisible = false">取消</ElButton
        ><ElButton type="primary" @click="saveGrants">保存授权</ElButton></template
      ></ElDialog
    >
  </main>
</template>
<script setup lang="ts">
  import type { Component } from 'vue'
  import { ElMessageBox } from 'element-plus'
  import { useAuth } from '@/hooks/core/useAuth'
  import {
    fetchResultViews,
    createResultView,
    replaceResultViewGrants,
    revokeResultView,
    setResultViewStatus,
    type ResultView,
    type ResultViewCreatePayload
  } from '@/api/resultViews'
  import { fetchPluginCatalog, type PluginCatalogItem } from '@/api/pluginCatalog'
  import { loadPluginComponent } from '@/plugins'
  import WorkflowSchemaFields from '@/components/workflow/WorkflowSchemaFields.vue'
  const { hasAuth } = useAuth(),
    views = ref<ResultView[]>([]),
    catalog = ref<PluginCatalogItem[]>([]),
    selected = ref<ResultView>(),
    resultComponent = shallowRef<Component>(),
    configComponent = shallowRef<Component>()
  const loading = ref(false),
    saving = ref(false),
    error = ref(''),
    createVisible = ref(false),
    pageID = ref(''),
    recipientIDs = ref<string[]>([])
  const pageChoices = computed(() =>
    catalog.value.flatMap((plugin) =>
      plugin.resultPages
        .filter((page) => hasAuth(page.permissionCode))
        .map((page) => ({ ...page, id: `${plugin.id}/${page.pageKey}`, pluginId: plugin.id }))
    )
  )
  const selectedPage = computed(() => pageChoices.value.find((page) => page.id === pageID.value))
  const form = reactive<ResultViewCreatePayload>({
    name: '',
    pluginId: '',
    pageKey: '',
    scope: {},
    filters: {},
    allowedActions: [],
    userIds: [],
    roleCodes: []
  })
  const pageTitle = (view: ResultView) =>
    catalog.value
      .find((plugin) => plugin.id === view.pluginId)
      ?.resultPages.find((page) => page.pageKey === view.pageKey)?.title ||
    `${view.pluginId}/${view.pageKey}`
  let requestID = 0
  const select = async (view: ResultView) => {
    const request = ++requestID
    selected.value = view
    resultComponent.value = undefined
    error.value = ''
    if (view.status !== 'active') return
    try {
      const component = await loadPluginComponent(view.pluginId, 'resultPages', view.pageKey)
      if (request === requestID) resultComponent.value = component
    } catch (cause: any) {
      if (request === requestID) error.value = cause.message
    }
  }
  const load = async () => {
    loading.value = true
    try {
      const [list, plugins] = await Promise.all([fetchResultViews(), fetchPluginCatalog()])
      views.value = list.items
      catalog.value = plugins.items
      const current =
        views.value.find((view) => view.id === selected.value?.id) ||
        views.value.find((view) => view.status === 'active')
      if (current) await select(current)
    } catch (cause: any) {
      error.value = cause.message
    } finally {
      loading.value = false
    }
  }
  const changePage = async () => {
    configComponent.value = undefined
    error.value = ''
    form.scope = {}
    form.filters = {}
    form.allowedActions = []
    const page = selectedPage.value
    if (!page) return
    form.pluginId = page.pluginId
    form.pageKey = page.pageKey
    if (page.configComponentEntry) {
      try {
        configComponent.value = await loadPluginComponent(
          page.pluginId,
          'resultConfigs',
          page.pageKey
        )
      } catch (cause: any) {
        error.value = cause.message
      }
    }
  }
  const openCreate = async () => {
    error.value = ''
    form.name = ''
    recipientIDs.value = []
    form.roleCodes = []
    pageID.value = pageChoices.value[0]?.id || ''
    await changePage()
    createVisible.value = true
  }
  const setFilter = (key: string, value: unknown) => {
    if (value === '' || value === undefined) delete form.filters[key]
    else form.filters[key] = value
  }
  const userIDs = (values: string[]) => {
    const ids = values.map(Number)
    if (ids.some((id) => !Number.isSafeInteger(id) || id <= 0))
      throw new Error('用户 ID 必须为正整数')
    return ids
  }
  const create = async () => {
    saving.value = true
    try {
      const view = await createResultView({ ...form, userIds: userIDs(recipientIDs.value) })
      createVisible.value = false
      await load()
      await select(view)
    } catch (cause: any) {
      error.value = cause.message
    } finally {
      saving.value = false
    }
  }
  const grantsVisible = ref(false),
    grantView = ref<ResultView>(),
    grantIDs = ref<string[]>([]),
    grantRoles = ref<string[]>([])
  const openGrants = (view: ResultView) => {
    grantView.value = view
    grantIDs.value = (view.userIds || []).map(String)
    grantRoles.value = [...(view.roleCodes || [])]
    grantsVisible.value = true
  }
  const saveGrants = async () => {
    if (!grantView.value) return
    await replaceResultViewGrants(grantView.value.id, {
      userIds: userIDs(grantIDs.value),
      roleCodes: grantRoles.value
    })
    grantsVisible.value = false
    await load()
  }
  const revoke = async (view: ResultView) => {
    await ElMessageBox.confirm('撤销后所有获授权用户立即失去此视图的访问权', '撤销结果视图')
    await revokeResultView(view.id)
    await load()
  }
  const changeStatus = async (view: ResultView) => {
    await setResultViewStatus(view.id, view.status === 'active' ? 'inactive' : 'active')
    await load()
  }
  onMounted(load)
  onBeforeUnmount(() => {
    requestID++
  })
</script>
<style scoped>
  .results-page {
    padding: 24px;
    color: var(--el-text-color-primary);
  }
  header,
  .view-toolbar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    margin-bottom: 20px;
  }
  h1 {
    font-size: 26px;
    margin: 0 0 8px;
  }
  p {
    color: var(--el-text-color-secondary);
  }
  .results-layout {
    display: grid;
    grid-template-columns: 240px minmax(0, 1fr);
    gap: 24px;
    min-height: 420px;
  }
  aside {
    display: flex;
    flex-direction: column;
    gap: 8px;
    border-right: 1px solid var(--el-border-color);
    padding-right: 20px;
  }
  aside button {
    text-align: left;
    padding: 14px;
    border: 1px solid transparent;
    border-radius: 5px;
    background: var(--el-fill-color-lighter);
    color: var(--el-text-color-primary);
    cursor: pointer;
  }
  aside strong,
  aside span {
    display: block;
  }
  aside span {
    font-size: 12px;
    margin-top: 6px;
    color: var(--el-text-color-secondary);
  }
  aside .selected {
    border-color: var(--el-color-primary);
  }
  aside button:focus-visible {
    outline: 2px solid var(--el-color-primary);
  }
  @media (max-width: 760px) {
    .results-page {
      padding: 12px;
    }
    .results-layout {
      grid-template-columns: 1fr;
    }
    aside {
      max-height: 230px;
      overflow: auto;
      border-right: 0;
      padding-right: 0;
    }
  }
</style>
