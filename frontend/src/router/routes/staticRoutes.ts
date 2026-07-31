/** 路由定义模块：staticRoutes。 */
import { AppRouteRecordRaw } from '@/utils/router'
import { useUserStore } from '@/store/modules/user'

const createPermissionGuard = (permissionCode: string) => {
  return () => {
    const permissions = useUserStore().info?.permissions || []
    return permissions.includes(permissionCode) ? true : { name: 'Exception403' }
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
    path: '/auth/register',
    name: 'Register',
    component: () => import('@views/auth/register/index.vue'),
    meta: { title: 'menus.register.title', isHideTab: true }
  },
  {
    path: '/auth/forget-password',
    name: 'ForgetPassword',
    component: () => import('@views/auth/forget-password/index.vue'),
    meta: { title: 'menus.forgetPassword.title', isHideTab: true }
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
    path: '/outside',
    component: () => import('@views/index/index.vue'),
    name: 'Outside',
    meta: { title: 'menus.outside.title' },
    children: [
      {
        path: '/outside/iframe/:path',
        name: 'Iframe',
        component: () => import('@/views/outside/Iframe.vue'),
        meta: { title: 'iframe' }
      }
    ]
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
        beforeEnter: createPermissionGuard('scheduler.workflow_definitions.create'),
        meta: {
          title: '创建工作流定义',
          isHideTab: true,
          isHide: true,
          activePath: '/scheduler/definition',
          actionList: [
            { title: '保存定义', permissionCode: 'scheduler.workflow_definitions.create' }
          ]
        }
      },
      {
        path: '/scheduler/workflow/:definitionId/edit',
        name: 'SchedulerWorkflowDefinitionEdit',
        component: () => import('@views/scheduler/workflow/editor/index.vue'),
        beforeEnter: createPermissionGuard('scheduler.workflow_definitions.update'),
        meta: {
          title: '编辑工作流定义',
          isHideTab: true,
          isHide: true,
          activePath: '/scheduler/definition',
          actionList: [
            { title: '保存定义', permissionCode: 'scheduler.workflow_definitions.update' }
          ]
        }
      },
      {
        path: '/scheduler/workflow/:definitionId/version',
        name: 'SchedulerWorkflowDefinitionVersion',
        redirect: (to) => `/scheduler/workflow/${to.params.definitionId}/edit`,
        beforeEnter: createPermissionGuard('scheduler.workflow_definitions.update'),
        meta: {
          title: '编辑工作流定义',
          isHideTab: true,
          isHide: true,
          activePath: '/scheduler/definition',
          actionList: [
            { title: '保存定义', permissionCode: 'scheduler.workflow_definitions.update' }
          ]
        }
      },
      {
        path: '/scheduler/execution/:executionId/detail',
        name: 'SchedulerWorkflowExecutionDetail',
        component: () => import('@views/scheduler/execution/detail/index.vue'),
        beforeEnter: createPermissionGuard('scheduler.workflow_executions.view'),
        meta: {
          title: '执行详情',
          isHideTab: true,
          isHide: true,
          activePath: '/scheduler/execution'
        }
      }
    ]
  }
]
