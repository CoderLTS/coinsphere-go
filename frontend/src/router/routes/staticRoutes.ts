/** 路由定义模块：staticRoutes。 */
import { AppRouteRecordRaw } from '@/utils/router'
import { useUserStore } from '@/store/modules/user'

const createAdminGuard = () => {
  return () => {
    return useUserStore().info?.roleCodes?.includes('R_SUPER') ? true : { name: 'Exception403' }
  }
}

export const staticRoutes: AppRouteRecordRaw[] = [
  {
    path: '/auth/login',
    name: 'Login',
    component: () => import('@views/auth/login/index.vue'),
    meta: { title: 'menus.login.title', isHideTab: true }
  },
  {
    path: '/403',
    name: 'Exception403',
    component: () => import('@views/exception/403/index.vue'),
    meta: { title: '403', isHideTab: true }
  },
  {
    path: '/:pathMatch(.*)*',
    name: 'Exception404',
    component: () => import('@views/exception/404/index.vue'),
    meta: { title: '404', isHideTab: true }
  },
  {
    path: '/500',
    name: 'Exception500',
    component: () => import('@views/exception/500/index.vue'),
    meta: { title: '500', isHideTab: true }
  },
  {
    path: '/',
    component: () => import('@views/index/index.vue'),
    name: 'WorkflowEditorShell',
    meta: { title: '工作流编辑器', isHideTab: true, isHide: true },
    children: [
      {
        path: '/scheduler/workflow/create',
        name: 'SchedulerWorkflowDefinitionCreate',
        component: () => import('@views/scheduler/workflow/editor/index.vue'),
        beforeEnter: createAdminGuard(),
        meta: {
          title: '创建工作流定义',
          isHideTab: true,
          isHide: true,
          activePath: '/scheduler/definition',
          actionList: [{ title: '保存定义', permissionCode: 'R_SUPER' }]
        }
      },
      {
        path: '/scheduler/workflow/:definitionId/edit',
        name: 'SchedulerWorkflowDefinitionEdit',
        component: () => import('@views/scheduler/workflow/editor/index.vue'),
        beforeEnter: createAdminGuard(),
        meta: {
          title: '编辑工作流定义',
          isHideTab: true,
          isHide: true,
          activePath: '/scheduler/definition',
          actionList: [{ title: '保存定义', permissionCode: 'R_SUPER' }]
        }
      },
      {
        path: '/scheduler/workflow/:definitionId/version',
        name: 'SchedulerWorkflowDefinitionVersion',
        redirect: (to) => `/scheduler/workflow/${to.params.definitionId}/edit`,
        beforeEnter: createAdminGuard(),
        meta: {
          title: '编辑工作流定义',
          isHideTab: true,
          isHide: true,
          activePath: '/scheduler/definition',
          actionList: [{ title: '保存定义', permissionCode: 'R_SUPER' }]
        }
      },
      {
        path: '/scheduler/execution',
        name: 'SchedulerWorkflowExecutions',
        component: () => import('@views/scheduler/execution/index.vue'),
        beforeEnter: createAdminGuard(),
        meta: {
          title: '历史运行日志',
          isHideTab: true,
          isHide: true,
          activePath: '/scheduler/definition'
        }
      },
      {
        path: '/scheduler/execution/:executionId/detail',
        name: 'SchedulerWorkflowExecutionDetail',
        component: () => import('@views/scheduler/execution/detail/index.vue'),
        beforeEnter: createAdminGuard(),
        meta: {
          title: '执行详情',
          isHideTab: true,
          isHide: true,
          activePath: '/scheduler/definition'
        }
      },
      {
        path: '/scheduler/execution/:runId/backtest',
        name: 'SchedulerWorkflowBacktestAnalysis',
        component: () => import('@views/scheduler/execution/backtest/index.vue'),
        beforeEnter: createAdminGuard(),
        meta: {
          title: '回测分析',
          isHideTab: true,
          isHide: true,
          activePath: '/scheduler/definition'
        }
      }
    ]
  },
  {
    path: '/results',
    name: 'Results',
    component: () => import('@views/results/index.vue'),
    meta: { title: 'menus.results.title' }
  }
]
