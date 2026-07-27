<!-- 角色管理页面或组件：role-permission-dialog。 -->
<template>
  <ElDialog
    v-model="visible"
    title="菜单权限"
    width="560px"
    align-center
    class="el-dialog-border"
    @close="handleClose"
  >
    <ElScrollbar height="70vh">
      <div v-loading="loading" class="role-permission-dialog">
        <ElTree
          ref="treeRef"
          :data="treeData"
          show-checkbox
          :check-strictly="true"
          node-key="nodeKey"
          :default-expand-all="isExpandAll"
          :props="defaultProps"
          @check="handleTreeCheck"
        >
          <template #default="{ data }">
            <div class="role-permission-dialog__node">
              <span class="role-permission-dialog__label">{{ data.label }}</span>
              <span
                v-if="data.isAction"
                class="role-permission-dialog__type role-permission-dialog__type--action"
              >
                按钮
              </span>
            </div>
          </template>
        </ElTree>
      </div>
    </ElScrollbar>

    <template #footer>
      <ElButton @click="toggleExpandAll">{{ isExpandAll ? '全部收起' : '全部展开' }}</ElButton>
      <ElButton @click="toggleSelectAll">{{ isSelectAll ? '取消全选' : '全部选择' }}</ElButton>
      <ElButton type="primary" :loading="saving" @click="savePermission">保存</ElButton>
    </template>
  </ElDialog>
</template>

<script setup lang="ts">
  import { nextTick } from 'vue'
  import { fetchGetManageMenuTree, fetchSaveRolePermissions } from '@/api/system'
  import { formatMenuTitle } from '@/utils/router'

  type RoleListItem = Api.System.RoleListItem

  interface Props {
    modelValue: boolean
    roleData?: RoleListItem
  }

  interface Emits {
    (e: 'update:modelValue', value: boolean): void
    (e: 'success'): void
  }

  interface TreeNode {
    nodeKey: string
    id?: number
    isAction: boolean
    label: string
    roles: string[]
    parentNodeKey?: string
    children?: TreeNode[]
  }

  const props = withDefaults(defineProps<Props>(), {
    modelValue: false,
    roleData: undefined
  })

  const emit = defineEmits<Emits>()

  const treeRef = ref()
  const loading = ref(false)
  const saving = ref(false)
  const isExpandAll = ref(true)
  const isSelectAll = ref(false)
  const treeData = ref<TreeNode[]>([])

  const visible = computed({
    get: () => props.modelValue,
    set: (value) => emit('update:modelValue', value)
  })

  const currentRoleCode = computed(() => props.roleData?.code || '')

  const defaultProps = {
    children: 'children',
    label: 'label'
  }

  const buildTree = (items: any[], parentNodeKey?: string): TreeNode[] => {
    return items.map((item) => {
      const menuId = typeof item.id === 'number' ? item.id : undefined
      const menuNodeKey = `menu-${String(menuId ?? item.name ?? Math.random().toString(36).slice(2, 8))}`
      const menuNode: TreeNode = {
        nodeKey: menuNodeKey,
        id: menuId,
        isAction: false,
        label: formatMenuTitle(String(item.meta?.title || item.name || '')),
        roles: Array.isArray(item.meta?.roles) ? item.meta.roles : [],
        parentNodeKey,
        children: []
      }

      const childMenus = Array.isArray(item.children) ? buildTree(item.children, menuNodeKey) : []
      const actionChildren: TreeNode[] = Array.isArray(item.meta?.actionList)
        ? item.meta.actionList.map((action: any) => ({
            nodeKey: `button-${String(action.id ?? `${item.name}-${action.permissionCode}`)}`,
            id: typeof action.id === 'number' ? action.id : undefined,
            isAction: true,
            label: action.title,
            roles: Array.isArray(action.roles) ? action.roles : [],
            parentNodeKey: menuNodeKey
          }))
        : []

      if (childMenus.length || actionChildren.length) {
        menuNode.children = [...childMenus, ...actionChildren]
      }

      return menuNode
    })
  }

  const collectCheckedKeysByRole = (nodes: TreeNode[], roleCode: string) => {
    const keys: string[] = []

    const visit = (items: TreeNode[]) => {
      items.forEach((item) => {
        if (item.roles.includes(roleCode)) {
          keys.push(item.nodeKey)
        }
        if (item.children?.length) {
          visit(item.children)
        }
      })
    }

    visit(nodes)
    return keys
  }

  const collectAllNodeKeys = (nodes: TreeNode[]) => {
    const keys: string[] = []

    const visit = (items: TreeNode[]) => {
      items.forEach((item) => {
        keys.push(item.nodeKey)
        if (item.children?.length) {
          visit(item.children)
        }
      })
    }

    visit(nodes)
    return keys
  }

  const nodeMap = computed(() => {
    const mapping = new Map<string, TreeNode>()

    const visit = (items: TreeNode[]) => {
      items.forEach((item) => {
        mapping.set(item.nodeKey, item)
        if (item.children?.length) {
          visit(item.children)
        }
      })
    }

    visit(treeData.value)
    return mapping
  })

  const syncSelectionState = () => {
    const tree = treeRef.value
    if (!tree) return
    const checkedKeys = tree.getCheckedKeys(false) as string[]
    const allKeys = collectAllNodeKeys(treeData.value)
    isSelectAll.value = checkedKeys.length === allKeys.length && allKeys.length > 0
  }

  const applyRoleCheckedState = async () => {
    await nextTick()
    const tree = treeRef.value
    if (!tree) return
    tree.setCheckedKeys(collectCheckedKeysByRole(treeData.value, currentRoleCode.value), false)
    syncSelectionState()
  }

  const loadPermissionTree = async () => {
    if (!props.roleData) return
    loading.value = true
    try {
      const menuTree = await fetchGetManageMenuTree()
      treeData.value = buildTree(menuTree as any[])
      await applyRoleCheckedState()
    } finally {
      loading.value = false
    }
  }

  watch(
    () => props.modelValue,
    async (visibleNow) => {
      if (!visibleNow || !props.roleData) return
      await loadPermissionTree()
    }
  )

  const handleClose = () => {
    visible.value = false
    treeRef.value?.setCheckedKeys([], false)
  }

  const savePermission = async () => {
    const roleId = props.roleData?.id
    const tree = treeRef.value
    if (!roleId || !tree) return

    const selectedKeys = new Set<string>([
      ...(tree.getCheckedKeys(false) as string[]),
      ...(tree.getHalfCheckedKeys() as string[])
    ])

    const selectedNodes = Array.from(selectedKeys)
      .map((key) => nodeMap.value.get(key))
      .filter((item): item is TreeNode => Boolean(item))

    const menuIds = new Set<number>()
    const buttonIds = new Set<number>()

    const appendAncestorMenus = (node: TreeNode) => {
      let currentKey = node.isAction ? node.parentNodeKey : node.nodeKey
      while (currentKey) {
        const currentNode = nodeMap.value.get(currentKey)
        if (!currentNode) break
        if (!currentNode.isAction && typeof currentNode.id === 'number') {
          menuIds.add(currentNode.id)
        }
        currentKey = currentNode.parentNodeKey
      }
    }

    selectedNodes.forEach((node) => {
      if (node.isAction) {
        if (typeof node.id === 'number') {
          buttonIds.add(node.id)
        }
      } else if (typeof node.id === 'number') {
        menuIds.add(node.id)
      }
      appendAncestorMenus(node)
    })

    saving.value = true
    try {
      await fetchSaveRolePermissions(roleId, {
        menuIds: Array.from(menuIds),
        buttonIds: Array.from(buttonIds)
      })
      emit('success')
      handleClose()
    } finally {
      saving.value = false
    }
  }

  const toggleExpandAll = () => {
    const tree = treeRef.value
    if (!tree) return
    Object.values(tree.store.nodesMap).forEach((node: any) => {
      node.expanded = !isExpandAll.value
    })
    isExpandAll.value = !isExpandAll.value
  }

  const toggleSelectAll = () => {
    const tree = treeRef.value
    if (!tree) return

    if (!isSelectAll.value) {
      tree.setCheckedKeys(collectAllNodeKeys(treeData.value), false)
    } else {
      tree.setCheckedKeys([], false)
    }

    isSelectAll.value = !isSelectAll.value
  }

  const handleTreeCheck = () => {
    syncSelectionState()
  }
</script>

<style scoped lang="scss">
  .role-permission-dialog {
    min-height: 360px;
    padding-right: 6px;
  }

  .role-permission-dialog__node {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
  }

  .role-permission-dialog__label {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .role-permission-dialog__type {
    flex-shrink: 0;
    padding: 2px 8px;
    border-radius: 999px;
    font-size: 11px;
    line-height: 1.4;
  }

  .role-permission-dialog__type--action {
    background: rgba(77, 140, 255, 0.1);
    color: #2563eb;
  }
</style>
