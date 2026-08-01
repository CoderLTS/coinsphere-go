<!-- 通用组件：art-horizontal-menu/index。 -->
<template>
  <div class="flex-1 overflow-hidden">
    <ElMenu
      :ellipsis="true"
      mode="horizontal"
      :default-active="routerPath"
      :text-color="isDark ? 'var(--art-gray-800)' : 'var(--art-gray-700)'"
      :popper-offset="-6"
      background-color="transparent"
      :show-timeout="50"
      :hide-timeout="50"
      popper-class="horizontal-menu-popper"
      class="w-full border-none"
    >
      <HorizontalSubmenu
        v-for="item in filteredMenuItems"
        :key="item.path"
        :item="item"
        :isMobile="false"
        :level="0"
      />
    </ElMenu>
  </div>
</template>

<script setup lang="ts">
  import type { AppRouteRecord } from '@/types/router'
  import HorizontalSubmenu from './widget/HorizontalSubmenu.vue'
  import { useSettingStore } from '@/store/modules/setting'

  defineOptions({ name: 'ArtHorizontalMenu' })

  const settingStore = useSettingStore()
  const { isDark } = storeToRefs(settingStore)

  interface Props {
    /** 菜单列表数据 */
    list: AppRouteRecord[]
  }

  const route = useRoute()

  const props = withDefaults(defineProps<Props>(), {
    list: () => []
  })

  /**
   * 过滤后的菜单项列表
   * 只显示未隐藏的菜单项
   */
  const filteredMenuItems = computed(() => {
    return filterMenuItems(props.list)
  })

  /**
   * 当前激活的路由路径
   * 用于菜单高亮显示
   */
  const routerPath = computed(() => String(route.meta.activePath || route.path))

  /**
   * 递归过滤菜单项，移除隐藏的菜单
   * 如果一个父菜单的所有子菜单都被隐藏，则父菜单也会被隐藏
   * @param items 菜单项数组
   * @returns 过滤后的菜单项数组
   */
  const filterMenuItems = (items: AppRouteRecord[]): AppRouteRecord[] => {
    return items
      .filter((item) => {
        // 如果当前项被隐藏，直接过滤掉
        if (item.meta.isHide) {
          return false
        }

        // 如果有子菜单，递归过滤子菜单
        if (item.children && item.children.length > 0) {
          const filteredChildren = filterMenuItems(item.children)
          // 如果所有子菜单都被过滤掉了，则隐藏父菜单
          return filteredChildren.length > 0
        }

        // 叶子节点且未被隐藏，保留
        return true
      })
      .map((item) => ({
        ...item,
        children: item.children ? filterMenuItems(item.children) : undefined
      }))
  }
</script>

<style scoped>
  /* Remove el-menu bottom border */
  :deep(.el-menu) {
    border-bottom: none !important;
  }

  /* Remove default styles for first-level menu items */
  :deep(.el-menu-item[tabindex='0']) {
    background-color: transparent !important;
    border: none !important;
  }

  :deep(.el-menu--horizontal > .el-sub-menu > .el-sub-menu__title),
  :deep(.el-menu--horizontal > .el-menu-item) {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 0 16px 0 10px !important;
    border: 0 !important;
    border-bottom: 0 !important;
    box-shadow: none !important;
  }

  :deep(.el-menu--horizontal > .el-sub-menu:hover .el-sub-menu__title),
  :deep(.el-menu--horizontal > .el-sub-menu.is-active .el-sub-menu__title),
  :deep(.el-menu--horizontal > .el-menu-item:hover),
  :deep(.el-menu--horizontal > .el-menu-item.is-active) {
    border-bottom: 0 !important;
    box-shadow: none !important;
  }

  :deep(.el-menu--horizontal > .el-sub-menu > .el-sub-menu__title .el-sub-menu__icon-arrow) {
    position: static;
    margin: 0 0 0 2px;
  }
</style>
