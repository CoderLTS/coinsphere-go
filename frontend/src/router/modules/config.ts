/** 静态路由模块：config。 */
import { AppRouteRecord } from '@/types/router'

export const configRoutes: AppRouteRecord = {
  path: '/config',
  name: 'ConfigCenter',
  component: '/index/index',
  meta: {
    title: 'menus.config.title',
    icon: 'ri:tools-line',
    roles: ['R_SUPER']
  },
  children: [
    {
      path: 'proxies',
      name: 'OutboundProxies',
      component: '/system/proxy',
      meta: {
        title: 'menus.config.proxies',
        icon: 'ri:route-line',
        keepAlive: true,
        roles: ['R_SUPER'],
        actionList: [
          { title: '新增', permissionCode: 'system.proxies.create' },
          { title: '编辑', permissionCode: 'system.proxies.update' },
          { title: '删除', permissionCode: 'system.proxies.delete' },
          { title: '检测', permissionCode: 'system.proxies.validate' }
        ]
      }
    },
    {
      path: 'ai-models',
      name: 'AiModelConfig',
      component: '/config/ai-model',
      meta: {
        title: 'menus.config.aiModels',
        icon: 'ri:brain-line',
        keepAlive: true,
        roles: ['R_SUPER']
      }
    },
    {
      path: 'plugins',
      name: 'Plugins',
      component: '/system/plugins',
      meta: {
        title: 'menus.config.plugins',
        icon: 'ri:puzzle-2-line',
        keepAlive: true,
        roles: ['R_SUPER']
      }
    }
  ]
}
