/** 静态路由模块：system。 */
import { AppRouteRecord } from '@/types/router'

export const systemRoutes: AppRouteRecord = {
  path: '/system',
  name: 'System',
  component: '/index/index',
  meta: {
    title: 'menus.system.title',
    icon: 'ri:user-3-line',
    roles: ['R_SUPER']
  },
  children: [
    {
      path: 'user',
      name: 'User',
      component: '/system/user',
      meta: {
        title: 'menus.system.user',
        keepAlive: true,
        roles: ['R_SUPER']
      }
    },
    {
      path: 'role',
      name: 'Role',
      component: '/system/role',
      meta: {
        title: 'menus.system.role',
        keepAlive: true,
        roles: ['R_SUPER']
      }
    },
    {
      path: 'user-center',
      name: 'UserCenter',
      component: '/system/user-center',
      meta: {
        title: 'menus.system.userCenter',
        isHide: true,
        keepAlive: true,
        isHideTab: true
      }
    },
    {
      path: 'menu',
      name: 'Menus',
      component: '/system/menu',
      meta: {
        title: 'menus.system.menu',
        keepAlive: true,
        roles: ['R_SUPER'],
        actionList: [
          { title: '新增', permissionCode: 'system.menus.create' },
          { title: '编辑', permissionCode: 'system.menus.update' },
          { title: '删除', permissionCode: 'system.menus.delete' }
        ]
      }
    },
    {
      path: 'ai-models',
      name: 'AiModelConfig',
      component: '/config/ai-model',
      meta: {
        title: 'menus.system.aiModels',
        icon: 'ri:brain-line',
        keepAlive: true,
        roles: ['R_SUPER']
      }
    }
  ]
}
