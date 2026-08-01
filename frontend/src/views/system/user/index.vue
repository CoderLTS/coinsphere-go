<!-- 用户管理页面或组件：index。 -->
<template>
  <div class="user-page art-full-height">
    <UserSearch v-model="searchForm" @search="handleSearch" @reset="handleResetSearch" />

    <ElCard class="art-table-card">
      <ArtTableHeader v-model:columns="columnChecks" :loading="loading" @refresh="refreshData">
        <template #left>
          <ElSpace wrap>
            <ElButton v-if="hasAuth('system.users.create')" @click="showDialog('add')" v-ripple>
              新增用户
            </ElButton>
          </ElSpace>
        </template>
      </ArtTableHeader>

      <ArtTable
        :loading="loading"
        :data="data"
        :columns="columns"
        :pagination="pagination"
        @selection-change="handleSelectionChange"
        @pagination:size-change="handleSizeChange"
        @pagination:current-change="handleCurrentChange"
      />

      <UserDialog
        v-model:visible="dialogVisible"
        :type="dialogType"
        :user-data="currentUserData"
        @submit="handleDialogSubmit"
      />
    </ElCard>
  </div>
</template>

<script setup lang="ts">
  import { Delete, Edit } from '@element-plus/icons-vue'
  import { ElButton, ElImage, ElMessageBox, ElTag } from 'element-plus'
  import defaultAvatar from '@imgs/user/avatar.webp'
  import { useTable } from '@/hooks/core/useTable'
  import { useAuth } from '@/hooks/core/useAuth'
  import { useUserStore } from '@/store/modules/user'
  import { fetchCreateUser, fetchDeleteUser, fetchGetUserList, fetchUpdateUser } from '@/api/system'
  import { DialogType } from '@/types'
  import UserSearch from './modules/user-search.vue'
  import UserDialog, { type UserFormPayload } from './modules/user-dialog.vue'

  defineOptions({ name: 'User' })

  type UserListItem = Api.System.UserListItem

  const { hasAuth } = useAuth()
  const userStore = useUserStore()

  const dialogType = ref<DialogType>('add')
  const dialogVisible = ref(false)
  const currentUserData = ref<Partial<UserListItem>>({})
  const selectedRows = ref<UserListItem[]>([])

  const searchForm = ref<Api.System.UserSearchParams>({
    username: undefined,
    gender: undefined,
    phone: undefined,
    email: undefined,
    isActive: undefined
  })

  const USER_STATUS_CONFIG = {
    true: { type: 'success' as const, text: '启用' },
    false: { type: 'info' as const, text: '停用' }
  } as const

  const GENDER_LABELS: Record<string, string> = {
    male: '男',
    female: '女',
    unknown: '未知'
  }

  const getUserStatusConfig = (isActive: boolean) =>
    USER_STATUS_CONFIG[String(isActive) as keyof typeof USER_STATUS_CONFIG] || {
      type: 'info' as const,
      text: '未知'
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

  const renderOperationButtons = (row: UserListItem) => {
    const buttons = []

    if (hasAuth('system.users.update')) {
      buttons.push(
        renderIconAction({
          icon: Edit,
          title: '编辑',
          onClick: () => showDialog('edit', row)
        })
      )
    }

    if (hasAuth('system.users.delete')) {
      buttons.push(
        renderIconAction({
          icon: Delete,
          title: '删除',
          type: 'danger',
          onClick: () => deleteUser(row)
        })
      )
    }

    return h('div', { class: 'table-actions' }, buttons)
  }

  const {
    columns,
    columnChecks,
    data,
    loading,
    pagination,
    getData,
    replaceSearchParams,
    resetSearchParams,
    handleSizeChange,
    handleCurrentChange,
    refreshData
  } = useTable({
    core: {
      apiFn: fetchGetUserList,
      apiParams: {
        current: 1,
        size: 20,
        ...searchForm.value
      },
      columnsFactory: () => [
        { type: 'selection' },
        { type: 'index', width: 60, label: '序号' },
        {
          prop: 'userInfo',
          label: '用户信息',
          minWidth: 260,
          formatter: (row: UserListItem) =>
            h('div', { class: 'user flex-c' }, [
              h(ElImage, {
                class: 'size-9.5 rounded-md',
                src: row.avatar || defaultAvatar,
                previewSrcList: row.avatar ? [row.avatar] : [defaultAvatar],
                previewTeleported: true,
                fit: 'cover'
              }),
              h('div', { class: 'ml-2' }, [
                h('p', { class: 'user-name' }, row.nickname || row.username),
                h('p', { class: 'email' }, row.username)
              ])
            ])
        },
        {
          prop: 'gender',
          label: '性别',
          width: 80,
          formatter: (row: UserListItem) => GENDER_LABELS[row.gender] || '未知'
        },
        { prop: 'phone', label: '手机号', minWidth: 130 },
        { prop: 'email', label: '邮箱', minWidth: 180 },
        {
          prop: 'isActive',
          label: '状态',
          width: 90,
          formatter: (row: UserListItem) => {
            const statusConfig = getUserStatusConfig(row.isActive)
            return h(ElTag, { type: statusConfig.type }, () => statusConfig.text)
          }
        },
        {
          prop: 'roleCodes',
          label: '角色',
          minWidth: 160,
          formatter: (row: UserListItem) => (row.roleCodes || []).join(' / ') || '--'
        },
        {
          prop: 'updatedAt',
          label: '更新时间',
          sortable: true,
          minWidth: 160
        },
        {
          prop: 'operation',
          label: '操作',
          width: 140,
          fixed: 'right',
          formatter: (row: UserListItem) => renderOperationButtons(row)
        }
      ]
    }
  })

  const handleSearch = (params: Api.System.UserSearchParams) => {
    replaceSearchParams(params)
    getData()
  }

  const handleResetSearch = () => {
    Object.assign(searchForm.value, {
      username: undefined,
      gender: undefined,
      phone: undefined,
      email: undefined,
      isActive: undefined
    })
    resetSearchParams()
  }

  const showDialog = (type: DialogType, row?: UserListItem): void => {
    dialogType.value = type
    currentUserData.value = row || {}
    dialogVisible.value = true
  }

  const deleteUser = async (row: UserListItem): Promise<void> => {
    await ElMessageBox.confirm(`确定要删除用户“${row.username}”吗？`, '删除用户', {
      confirmButtonText: '确定',
      cancelButtonText: '取消',
      type: 'warning'
    })
    await fetchDeleteUser(row.id)
    getData()
  }

  const handleDialogSubmit = async (payload: UserFormPayload) => {
    let savedUser: UserListItem | undefined

    if (dialogType.value === 'add') {
      savedUser = await fetchCreateUser(payload)
    } else if (currentUserData.value.id) {
      savedUser = await fetchUpdateUser(Number(currentUserData.value.id), payload)
    }

    if (savedUser && Number(userStore.info.userId) === savedUser.id) {
      const currentInfo = userStore.getUserInfo
      userStore.setUserInfo({
        permissions: currentInfo.permissions || [],
        roleCodes: currentInfo.roleCodes || [],
        userId: currentInfo.userId || savedUser.id,
        username: savedUser.username,
        email: savedUser.email,
        avatar: savedUser.avatar,
        accessMode: currentInfo.accessMode || 'authenticated'
      })
    }

    dialogVisible.value = false
    currentUserData.value = {}
    getData()
  }

  const handleSelectionChange = (selection: UserListItem[]): void => {
    selectedRows.value = selection
  }
</script>

<style scoped lang="scss">
  .table-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    justify-content: center;
  }
</style>
