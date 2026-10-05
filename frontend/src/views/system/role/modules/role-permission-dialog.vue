<template>
  <ElDialog v-model="visible" title="角色能力与导航" width="min(740px, 95vw)"
    ><ElTabs v-model="tab"
      ><ElTabPane label="能力权限" name="capabilities"
        ><p>能力决定可执行的操作；资源授权进一步限制工作流范围。</p
        ><ElInput v-model="search" placeholder="搜索能力" clearable /><ElCheckboxGroup
          v-model="permissionCodes"
          class="capabilities"
          ><ElCheckbox
            v-for="capability in filtered"
            :key="capability.code"
            :value="capability.code"
            :disabled="!capability.grantable"
            ><span>{{ capability.title }}</span
            ><small
              >{{ capability.code }}{{ capability.protected ? ' · 受保护' : '' }}</small
            ></ElCheckbox
          ></ElCheckboxGroup
        ></ElTabPane
      ><ElTabPane label="导航菜单" name="navigation"
        ><p>菜单只控制导航布局，选择菜单不会授予能力。</p
        ><ElTree
          ref="tree"
          :data="navigation"
          node-key="id"
          :props="{ children: 'children', label: 'title' }"
          show-checkbox
          check-strictly
          default-expand-all /></ElTabPane></ElTabs
    ><template #footer
      ><ElButton @click="visible = false">取消</ElButton
      ><ElButton type="primary" :loading="saving" @click="save">保存角色设置</ElButton></template
    ></ElDialog
  >
</template>
<script setup lang="ts">
  import {
    fetchCapabilities,
    fetchGetManageMenuTree,
    fetchSaveRolePermissions,
    type Capability
  } from '@/api/system'
  import { formatMenuTitle } from '@/utils/router'
  const props = defineProps<{ modelValue: boolean; roleData?: Api.System.RoleListItem }>()
  const emit = defineEmits<{
    (event: 'update:modelValue', value: boolean): void
    (event: 'success'): void
  }>()
  const visible = computed({
      get: () => props.modelValue,
      set: (value) => emit('update:modelValue', value)
    }),
    saving = ref(false),
    tab = ref('capabilities'),
    search = ref('')
  const capabilities = ref<Capability[]>([]),
    permissionCodes = ref<string[]>([]),
    navigation = ref<any[]>([]),
    tree = ref<any>()
  const filtered = computed(() =>
    capabilities.value.filter((item) =>
      `${item.title} ${item.code}`.toLowerCase().includes(search.value.toLowerCase())
    )
  )
  const menus = (rows: any[]): any[] =>
    rows.map((row) => ({
      id: row.id,
      title: formatMenuTitle(String(row.meta?.title || row.name)),
      roles: row.meta?.roles || [],
      children: menus(row.children || [])
    }))
  watch(
    () => props.modelValue,
    async (opened) => {
      if (!opened || !props.roleData) return
      const [catalog, rows] = await Promise.all([fetchCapabilities(), fetchGetManageMenuTree()])
      capabilities.value = catalog.items
      permissionCodes.value = [...(props.roleData.permissionCodes || [])]
      navigation.value = menus(rows)
      await nextTick()
      const selected: number[] = []
      const visit = (items: any[]) =>
        items.forEach((item) => {
          if (item.roles.includes(props.roleData?.code)) selected.push(item.id)
          visit(item.children)
        })
      visit(navigation.value)
      tree.value?.setCheckedKeys(selected)
    }
  )
  const save = async () => {
    if (!props.roleData) return
    saving.value = true
    try {
      await fetchSaveRolePermissions(props.roleData.id, {
        menuIds: tree.value?.getCheckedKeys() || [],
        permissionCodes: permissionCodes.value
      })
      emit('success')
      visible.value = false
    } finally {
      saving.value = false
    }
  }
</script>
<style scoped>
  p,
  small {
    color: var(--el-text-color-secondary);
  }
  .capabilities {
    display: grid;
    grid-template-columns: 1fr 1fr;
    max-height: 55vh;
    overflow: auto;
    margin-top: 20px;
    gap: 20px;
  }
  .capabilities :deep(.el-checkbox) {
    height: auto;
    margin-right: 0;
  }
  .capabilities small {
    display: block;
    font-size: 11px;
  }
  @media (max-width: 640px) {
    .capabilities {
      grid-template-columns: 1fr;
    }
  }
</style>
