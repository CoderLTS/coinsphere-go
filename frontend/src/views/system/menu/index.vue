<!-- 菜单管理页面或组件：index。 -->
<template>
  <div class="menu-page art-full-height">
    <ArtSearchBar
      v-model="formFilters"
      :items="formItems"
      :showExpand="false"
      @reset="handleReset"
      @search="handleSearch"
    />

    <ElCard class="art-table-card">
      <ArtTableHeader
        v-model:columns="columnChecks"
        :showZebra="false"
        :loading="loading"
        @refresh="handleRefresh"
      >
        <template #left>
          <ElButton v-if="hasAuth('system.menus.create')" v-ripple @click="handleAddMenu">
            新增菜单
          </ElButton>
          <ElButton v-ripple @click="toggleExpand">
            {{ isExpanded ? '收起' : '展开' }}
          </ElButton>
        </template>
      </ArtTableHeader>

      <ArtTable
        ref="tableRef"
        rowKey="rowKey"
        :loading="loading"
        :columns="columns"
        :data="filteredTableData"
        :stripe="false"
        :tree-props="{ children: 'children', hasChildren: 'hasChildren' }"
        :default-expand-all="false"
      />

      <MenuDialog
        v-model:visible="dialogVisible"
        :type="dialogType"
        :edit-data="editData"
        :menu-tree="tableData"
        @submit="handleSubmit"
      />
    </ElCard>
  </div>
</template>

<script setup lang="ts">
  import { Delete, Edit, Plus } from '@element-plus/icons-vue'
  import { ElButton, ElMessageBox, ElTag } from 'element-plus'
  import { useAuth } from '@/hooks/core/useAuth'
  import { useTableColumns } from '@/hooks/core/useTableColumns'
  import type { AppRouteRecord } from '@/types/router'
  import { formatMenuTitle } from '@/utils/router'
  import {
    fetchCreateMenu,
    fetchCreateMenuButton,
    fetchDeleteMenu,
    fetchDeleteMenuButton,
    fetchGetManageMenuTree,
    fetchUpdateMenu,
    fetchUpdateMenuButton
  } from '@/api/system'
  import MenuDialog, { type MenuDialogPayload } from './modules/menu-dialog.vue'

  defineOptions({ name: 'Menus' })

  interface MenuActionItem {
    id?: number
    title: string
    permissionCode: string
    i18nKey?: string
    i18nTexts?: Api.System.I18nTexts
    sort?: number
    roles?: string[]
    updatedAt?: string
  }

  interface MenuTableRow extends AppRouteRecord {
    rowKey?: string
    parentMenuId?: number
    updatedAt?: string
    meta: AppRouteRecord['meta'] & {
      isEnable?: boolean
      sort?: number
      roles?: string[]
      actionList?: MenuActionItem[]
      permissionCode?: string
    }
    children?: MenuTableRow[]
  }

  const { hasAuth } = useAuth()

  const loading = ref(false)
  const isExpanded = ref(false)
  const tableRef = ref()
  const dialogVisible = ref(false)
  const dialogType = ref<'menu' | 'button'>('menu')
  const editData = ref<Record<string, any> | null>(null)

  const initialSearchState = {
    name: '',
    route: '',
    permissionCode: ''
  }

  const formFilters = reactive({ ...initialSearchState })
  const appliedFilters = reactive({ ...initialSearchState })
  const tableData = ref<MenuTableRow[]>([])

  const formItems = computed(() => [
    {
      label: '菜单名称',
      key: 'name',
      type: 'input',
      props: { clearable: true }
    },
    {
      label: '路由地址',
      key: 'route',
      type: 'input',
      props: { clearable: true }
    },
    {
      label: '权限码',
      key: 'permissionCode',
      type: 'input',
      props: { clearable: true }
    }
  ])

  onMounted(() => {
    getMenuList()
  })

  const deepClone = <T,>(obj: T): T => JSON.parse(JSON.stringify(obj)) as T

  const getDisplayTitle = (
    i18nKey: string | undefined,
    fallbackTitle: string | undefined
  ): string => {
    if (i18nKey) {
      const translated = formatMenuTitle(i18nKey)
      if (translated !== i18nKey) {
        return translated
      }
    }
    return fallbackTitle || ''
  }

  const getMenuRoute = (row: MenuTableRow): string => {
    if (row.meta?.isAuthButton) {
      return '--'
    }
    return row.meta?.link || row.path || '--'
  }

  const getMenuPermissionCode = (row: MenuTableRow): string => {
    return row.meta?.permissionCode || '--'
  }

  const renderIconAction = (options: {
    icon: typeof Edit
    title: string
    onClick: () => void
    type?: '' | 'primary' | 'danger'
  }) =>
    h(ElButton, {
      class: 'icon-action',
      size: 'small',
      circle: true,
      plain: true,
      icon: options.icon,
      type: options.type,
      title: options.title,
      onClick: options.onClick
    })

  const getMenuList = async (): Promise<void> => {
    loading.value = true
    try {
      tableData.value = (await fetchGetManageMenuTree()) as MenuTableRow[]
    } finally {
      loading.value = false
    }
  }

  const getMenuTypeTag = (
    row: MenuTableRow
  ): 'primary' | 'success' | 'warning' | 'info' | 'danger' => {
    if (row.meta?.isAuthButton) return 'danger'
    if (row.children?.some((child) => !child.meta?.isAuthButton)) return 'info'
    if (row.meta?.link && row.meta?.isIframe) return 'success'
    if (row.path) return 'primary'
    if (row.meta?.link) return 'warning'
    return 'info'
  }

  const getMenuTypeText = (row: MenuTableRow): string => {
    if (row.meta?.isAuthButton) return '按钮'
    if (row.children?.some((child) => !child.meta?.isAuthButton)) return '目录'
    if (row.meta?.link && row.meta?.isIframe) return '内嵌'
    return '菜单'
  }

  const renderOperationButtons = (row: MenuTableRow) => {
    const buttons = []

    if (row.meta?.isAuthButton) {
      if (hasAuth('system.menus.update')) {
        buttons.push(
          renderIconAction({
            icon: Edit,
            title: '编辑',
            onClick: () => handleEditAction(row)
          })
        )
      }
      if (hasAuth('system.menus.delete')) {
        buttons.push(
          renderIconAction({
            icon: Delete,
            title: '删除',
            type: 'danger',
            onClick: () => handleDeleteAction(row)
          })
        )
      }
      return h('div', { class: 'table-actions table-actions--right' }, buttons)
    }

    if (hasAuth('system.menus.create')) {
      buttons.push(
        renderIconAction({
          icon: Plus,
          type: 'primary',
          title: '新增按钮',
          onClick: () => handleAddAction(row)
        })
      )
    }
    if (hasAuth('system.menus.update')) {
      buttons.push(
        renderIconAction({
          icon: Edit,
          title: '编辑',
          onClick: () => handleEditMenu(row)
        })
      )
    }
    if (hasAuth('system.menus.delete')) {
      buttons.push(
        renderIconAction({
          icon: Delete,
          title: '删除',
          type: 'danger',
          onClick: () => handleDeleteMenu(row)
        })
      )
    }

    return h('div', { class: 'table-actions table-actions--right' }, buttons)
  }

  const { columnChecks, columns } = useTableColumns(() => [
    {
      prop: 'meta.title',
      label: '菜单名称',
      minWidth: 180,
      formatter: (row: MenuTableRow) =>
        getDisplayTitle(String(row.meta?.i18nKey || ''), String(row.meta?.title || ''))
    },
    {
      prop: 'type',
      label: '菜单类型',
      width: 100,
      formatter: (row: MenuTableRow) =>
        h(ElTag, { type: getMenuTypeTag(row) }, () => getMenuTypeText(row))
    },
    {
      prop: 'path',
      label: '路由地址',
      minWidth: 220,
      formatter: (row: MenuTableRow) => getMenuRoute(row)
    },
    {
      prop: 'permissionCode',
      label: '权限码',
      minWidth: 220,
      formatter: (row: MenuTableRow) => getMenuPermissionCode(row)
    },
    {
      prop: 'roles',
      label: '可见角色',
      minWidth: 180,
      formatter: (row: MenuTableRow) => (row.meta?.roles || []).join(' / ') || '--'
    },
    {
      prop: 'updatedAt',
      label: '编辑时间',
      minWidth: 160,
      formatter: (row: MenuTableRow) => row.updatedAt || '--'
    },
    {
      prop: 'status',
      label: '状态',
      width: 90,
      formatter: (row: MenuTableRow) =>
        h(ElTag, { type: row.meta?.isEnable === false ? 'info' : 'success' }, () =>
          row.meta?.isEnable === false ? '停用' : '启用'
        )
    },
    {
      prop: 'operation',
      label: '操作',
      width: 180,
      align: 'right',
      formatter: (row: MenuTableRow) => renderOperationButtons(row)
    }
  ])

  const handleReset = (): void => {
    Object.assign(formFilters, { ...initialSearchState })
    Object.assign(appliedFilters, { ...initialSearchState })
  }

  const handleSearch = (): void => {
    Object.assign(appliedFilters, { ...formFilters })
  }

  const handleRefresh = (): void => {
    getMenuList()
  }

  const convertActionListToChildren = (items: MenuTableRow[]): MenuTableRow[] => {
    return items.map((item) => {
      const clonedItem = deepClone(item)

      if (clonedItem.children?.length) {
        clonedItem.children = convertActionListToChildren(clonedItem.children)
      }

      if (item.meta?.actionList?.length) {
        const actionChildren: MenuTableRow[] = item.meta.actionList.map((action) => ({
          id: action.id,
          parentId: item.id,
          parentMenuId: item.id,
          path: '',
          name: `${String(item.name)}_action_${action.permissionCode}`,
          component: '',
          updatedAt: action.updatedAt,
          meta: {
            title: action.title,
            i18nKey: action.i18nKey,
            i18nTexts: action.i18nTexts,
            permissionCode: action.permissionCode,
            isAuthButton: true,
            parentPath: item.path,
            roles: action.roles || [],
            sort: action.sort,
            isEnable: true
          }
        }))

        clonedItem.children = clonedItem.children?.length
          ? [...clonedItem.children, ...actionChildren]
          : actionChildren
      }

      return clonedItem
    })
  }

  const searchMenu = (items: MenuTableRow[]): MenuTableRow[] => {
    const results: MenuTableRow[] = []

    for (const item of items) {
      const searchName = appliedFilters.name.toLowerCase().trim()
      const searchRoute = appliedFilters.route.toLowerCase().trim()
      const searchPermissionCode = appliedFilters.permissionCode.toLowerCase().trim()
      const menuTitle = getDisplayTitle(
        String(item.meta?.i18nKey || ''),
        String(item.meta?.title || '')
      ).toLowerCase()
      const routeValue = (
        item.meta?.isAuthButton ? '' : item.meta?.link || item.path || ''
      ).toLowerCase()
      const permissionValue = String(item.meta?.permissionCode || '').toLowerCase()
      const nameMatch = !searchName || menuTitle.includes(searchName)
      const routeMatch = !searchRoute || routeValue.includes(searchRoute)
      const permissionMatch =
        !searchPermissionCode || permissionValue.includes(searchPermissionCode)

      if (item.children?.length) {
        const matchedChildren = searchMenu(item.children)
        if (matchedChildren.length > 0) {
          const clonedItem = deepClone(item)
          clonedItem.children = matchedChildren
          results.push(clonedItem)
          continue
        }
      }

      if (nameMatch && routeMatch && permissionMatch) {
        results.push(deepClone(item))
      }
    }

    return results
  }

  const attachRowKeys = (items: MenuTableRow[]): MenuTableRow[] => {
    return items.map((item) => {
      const rowKey = item.meta?.isAuthButton ? `button-${item.id}` : `menu-${item.id}`
      return {
        ...item,
        rowKey,
        children: item.children?.length ? attachRowKeys(item.children) : undefined
      }
    })
  }

  const filteredTableData = computed(() => {
    const searchedData = searchMenu(tableData.value)
    return attachRowKeys(convertActionListToChildren(searchedData))
  })

  const handleAddMenu = (): void => {
    dialogType.value = 'menu'
    editData.value = null
    dialogVisible.value = true
  }

  const handleAddAction = (row: MenuTableRow): void => {
    dialogType.value = 'button'
    editData.value = {
      menuId: Number(row.id),
      parentMenuTitle: getDisplayTitle(
        String(row.meta?.i18nKey || ''),
        String(row.meta?.title || '')
      ),
      roleCodes: [...(row.meta?.roles || [])]
    }
    dialogVisible.value = true
  }

  const handleEditMenu = (row: MenuTableRow): void => {
    dialogType.value = 'menu'
    editData.value = deepClone(row)
    dialogVisible.value = true
  }

  const handleEditAction = (row: MenuTableRow): void => {
    dialogType.value = 'button'
    editData.value = {
      id: row.id,
      menuId: row.parentMenuId,
      title: row.meta?.title,
      i18nKey: row.meta?.i18nKey,
      i18nTexts: row.meta?.i18nTexts,
      permissionCode: row.meta?.permissionCode,
      sort: row.meta?.sort || 1,
      roleCodes: [...(row.meta?.roles || [])]
    }
    dialogVisible.value = true
  }

  const handleSubmit = async (payload: MenuDialogPayload): Promise<void> => {
    if (payload.menuType === 'menu') {
      const params = {
        parentId: payload.parentId || null,
        title: payload.title,
        i18nKey: payload.i18nKey,
        i18nTexts: {
          zh: payload.i18nTexts.zh,
          en: payload.i18nTexts.en
        },
        name: payload.name,
        path: payload.path,
        component: payload.component,
        permissionCode: payload.permissionCode || null,
        icon: payload.icon,
        sort: payload.sort,
        isActive: payload.isEnable,
        keepAlive: payload.keepAlive,
        isHidden: payload.isHide,
        hideTab: payload.isHideTab,
        externalUrl: payload.link,
        useIframe: payload.isIframe,
        badgeLabel: payload.badgeText,
        fixedTab: payload.fixedTab,
        activeMenuPath: payload.activePath,
        roleCodes: payload.roleCodes,
        isFullScreen: payload.isFullPage
      }
      if (payload.id) {
        await fetchUpdateMenu(payload.id, params)
      } else {
        await fetchCreateMenu(params)
      }
    } else {
      const params = {
        menuId: payload.menuId,
        title: payload.title,
        i18nKey: payload.i18nKey,
        i18nTexts: {
          zh: payload.i18nTexts.zh,
          en: payload.i18nTexts.en
        },
        permissionCode: payload.permissionCode,
        sort: payload.sort,
        roleCodes: payload.roleCodes
      }
      if (payload.id) {
        await fetchUpdateMenuButton(payload.id, params)
      } else {
        await fetchCreateMenuButton(params)
      }
    }

    dialogVisible.value = false
    editData.value = null
    await getMenuList()
  }

  const handleDeleteMenu = async (row: MenuTableRow): Promise<void> => {
    const title = getDisplayTitle(String(row.meta?.i18nKey || ''), String(row.meta?.title || ''))
    await ElMessageBox.confirm(`确定要删除菜单“${title}”吗？`, '提示', {
      confirmButtonText: '确定',
      cancelButtonText: '取消',
      type: 'warning'
    })
    await fetchDeleteMenu(Number(row.id))
    await getMenuList()
  }

  const handleDeleteAction = async (row: MenuTableRow): Promise<void> => {
    const title = getDisplayTitle(String(row.meta?.i18nKey || ''), String(row.meta?.title || ''))
    await ElMessageBox.confirm(`确定要删除按钮“${title}”吗？`, '提示', {
      confirmButtonText: '确定',
      cancelButtonText: '取消',
      type: 'warning'
    })
    await fetchDeleteMenuButton(Number(row.id))
    await getMenuList()
  }

  const toggleExpand = (): void => {
    isExpanded.value = !isExpanded.value
    nextTick(() => {
      if (tableRef.value?.elTableRef && filteredTableData.value) {
        const processRows = (rows: MenuTableRow[]) => {
          rows.forEach((row) => {
            if (row.children?.length) {
              tableRef.value.elTableRef.toggleRowExpansion(row, isExpanded.value)
              processRows(row.children)
            }
          })
        }
        processRows(filteredTableData.value)
      }
    })
  }
</script>

<style scoped lang="scss">
  .table-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    align-items: center;
    justify-content: center;
  }

  .table-actions--right {
    justify-content: flex-end;
  }
</style>
