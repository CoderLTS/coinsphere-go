<!-- 角色管理页面或组件：index。 -->
<template>
  <div class="art-full-height">
    <RoleSearch
      v-show="showSearchBar"
      v-model="searchForm"
      @search="handleSearch"
      @reset="resetSearchParams"
    />

    <ElCard class="art-table-card" :style="{ 'margin-top': showSearchBar ? '12px' : '0' }">
      <ArtTableHeader
        v-model:columns="columnChecks"
        v-model:showSearchBar="showSearchBar"
        :loading="loading"
        @refresh="refreshData"
      >
        <template #left>
          <ElSpace wrap>
            <ElButton v-if="hasAuth('system.roles.create')" @click="showDialog('add')" v-ripple>
              新增角色
            </ElButton>
          </ElSpace>
        </template>
      </ArtTableHeader>

      <ArtTable
        :loading="loading"
        :data="data"
        :columns="columns"
        :pagination="pagination"
        @pagination:size-change="handleSizeChange"
        @pagination:current-change="handleCurrentChange"
      />
    </ElCard>

    <RoleEditDialog
      v-model="dialogVisible"
      :dialog-type="dialogType"
      :role-data="currentRoleData"
      @success="refreshData"
    />

    <RolePermissionDialog
      v-model="permissionDialog"
      :role-data="currentRoleData"
      @success="refreshData"
    />
  </div>
</template>

<script setup lang="ts">
  import { Delete, Edit, Key } from '@element-plus/icons-vue'
  import { ElButton, ElMessage, ElMessageBox, ElTag } from 'element-plus'
  import { useAuth } from '@/hooks/core/useAuth'
  import { useTable } from '@/hooks/core/useTable'
  import { fetchDeleteRole, fetchGetRoleList } from '@/api/system'
  import RoleEditDialog from './modules/role-edit-dialog.vue'
  import RolePermissionDialog from './modules/role-permission-dialog.vue'
  import RoleSearch from './modules/role-search.vue'

  defineOptions({ name: 'Role' })

  type RoleListItem = Api.System.RoleListItem
  type RoleSearchFormParams = Api.System.RoleSearchParams & {
    daterange?: string[]
  }

  const { hasAuth } = useAuth()

  const searchForm = ref<RoleSearchFormParams>({
    displayName: undefined,
    code: undefined,
    description: undefined,
    isEnabled: undefined,
    daterange: undefined
  })

  const showSearchBar = ref(false)
  const dialogVisible = ref(false)
  const permissionDialog = ref(false)
  const currentRoleData = ref<RoleListItem | undefined>(undefined)
  const dialogType = ref<'add' | 'edit'>('add')

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

  const {
    columns,
    columnChecks,
    data,
    loading,
    pagination,
    getData,
    searchParams,
    resetSearchParams,
    handleSizeChange,
    handleCurrentChange,
    refreshData
  } = useTable({
    core: {
      apiFn: fetchGetRoleList,
      apiParams: {
        current: 1,
        size: 20
      },
      excludeParams: ['daterange'],
      columnsFactory: () => [
        {
          prop: 'id',
          label: '角色 ID',
          width: 100
        },
        {
          prop: 'displayName',
          label: '角色名称',
          minWidth: 120
        },
        {
          prop: 'code',
          label: '角色编码',
          minWidth: 120
        },
        {
          prop: 'description',
          label: '角色描述',
          minWidth: 150,
          showOverflowTooltip: true
        },
        {
          prop: 'isEnabled',
          label: '状态',
          width: 100,
          formatter: (row: RoleListItem) => {
            const statusConfig = row.isEnabled
              ? { type: 'success', text: '启用' }
              : { type: 'warning', text: '禁用' }
            return h(
              ElTag,
              { type: statusConfig.type as 'success' | 'warning' },
              () => statusConfig.text
            )
          }
        },
        {
          prop: 'createdAt',
          label: '创建日期',
          width: 180,
          sortable: true
        },
        {
          prop: 'operation',
          label: '操作',
          width: 160,
          fixed: 'right',
          formatter: (row: RoleListItem) =>
            row.isSystem
              ? h('span', { class: 'text-[var(--el-text-color-secondary)] text-xs' }, '系统内置')
              : h('div', { class: 'table-actions' }, [
                  hasAuth('system.roles.assign_permissions')
                    ? renderIconAction({
                        icon: Key,
                        title: '分配权限',
                        type: 'primary',
                        onClick: () => showPermissionDialog(row)
                      })
                    : null,
                  hasAuth('system.roles.update')
                    ? renderIconAction({
                        icon: Edit,
                        title: '编辑',
                        onClick: () => showDialog('edit', row)
                      })
                    : null,
                  hasAuth('system.roles.delete')
                    ? renderIconAction({
                        icon: Delete,
                        title: '删除',
                        type: 'danger',
                        onClick: () => deleteRole(row)
                      })
                    : null
                ].filter(Boolean))
        }
      ]
    }
  })

  const showDialog = (type: 'add' | 'edit', row?: RoleListItem) => {
    dialogVisible.value = true
    dialogType.value = type
    currentRoleData.value = row
  }

  const handleSearch = (params: RoleSearchFormParams) => {
    const { daterange, ...filtersParams } = params
    const [startTime, endTime] = Array.isArray(daterange) ? daterange : [null, null]
    Object.assign(searchParams, { ...filtersParams, startTime, endTime })
    getData()
  }

  const showPermissionDialog = (row?: RoleListItem) => {
    permissionDialog.value = true
    currentRoleData.value = row
  }

  const deleteRole = async (row: RoleListItem) => {
    try {
      await ElMessageBox.confirm(
        `确定删除角色“${row.displayName}”吗？此操作不可恢复。`,
        '删除确认',
        {
          confirmButtonText: '确定',
          cancelButtonText: '取消',
          type: 'warning'
        }
      )
    } catch {
      ElMessage.info('已取消删除')
      return
    }

    await fetchDeleteRole(row.id)
    refreshData()
  }
</script>

<style scoped lang="scss">
  .table-actions {
    display: flex;
    justify-content: center;
    gap: 8px;
    flex-wrap: wrap;
  }
</style>
