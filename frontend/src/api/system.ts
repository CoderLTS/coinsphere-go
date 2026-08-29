/** 前端接口封装：system。 */
import request from '@/utils/http'
import { AppRouteRecord } from '@/types/router'

export function fetchGetUserList(params: Api.System.UserSearchParams) {
  return request.get<Api.System.UserList>({
    url: '/api/v1/admin/users',
    params
  })
}

export function fetchGetRoleList(params: Api.System.RoleSearchParams) {
  return request.get<Api.System.RoleList>({
    url: '/api/v1/system/roles',
    params
  })
}

export function fetchCreateRole(params: Api.System.RoleUpsertPayload) {
  return request.post<Api.System.RoleListItem>({
    url: '/api/v1/system/roles',
    params,
    showSuccessMessage: true
  })
}

export function fetchUpdateRole(roleId: number, params: Api.System.RoleUpsertPayload) {
  return request.put<Api.System.RoleListItem>({
    url: `/api/v1/system/roles/${roleId}`,
    params,
    showSuccessMessage: true
  })
}

export function fetchDeleteRole(roleId: number) {
  return request.del<void>({
    url: `/api/v1/system/roles/${roleId}`,
    showSuccessMessage: true
  })
}

export function fetchSaveRolePermissions(roleId: number, params: Api.System.RolePermissionPayload) {
  return request.put<void>({
    url: `/api/v1/system/roles/${roleId}/permissions`,
    params,
    showSuccessMessage: true
  })
}

export function fetchGetMenuList() {
  return request.get<AppRouteRecord[]>({
    url: '/api/v1/system/menus'
  })
}

export function fetchGetManageMenuTree() {
  return request.get<AppRouteRecord[]>({
    url: '/api/v1/system/menus/manage-tree'
  })
}

export function fetchGetMenuI18nDict() {
  return request.get<Api.System.MenuI18nDict>({
    url: '/api/v1/system/i18n-dictionaries',
    params: { scope: 'menu' }
  })
}

export function fetchCreateUser(params: Record<string, any>) {
  return request.post<Api.System.UserListItem>({
    url: '/api/v1/admin/users',
    params,
    showSuccessMessage: true
  })
}

export function fetchUpdateUser(userId: number, params: Record<string, any>) {
  return request.put<Api.System.UserListItem>({
    url: `/api/v1/admin/users/${userId}`,
    params,
    showSuccessMessage: true
  })
}

export function fetchDeleteUser(userId: number) {
  return request.del<void>({
    url: `/api/v1/admin/users/${userId}`,
    showSuccessMessage: true
  })
}

export function fetchUploadAvatarAsset(file: File) {
  const formData = new FormData()
  formData.append('avatar', file)
  return request.post<{ url: string }>({
    url: '/api/v1/system/uploads/avatars',
    data: formData,
    showSuccessMessage: true
  })
}

export function fetchUploadUserAvatar(file: File) {
  return fetchUploadAvatarAsset(file)
}

export function fetchCreateMenu(params: Record<string, any>) {
  return request.post<{ id: number }>({
    url: '/api/v1/system/menus',
    params,
    showSuccessMessage: true
  })
}

export function fetchUpdateMenu(menuId: number, params: Record<string, any>) {
  return request.put<{ id: number }>({
    url: `/api/v1/system/menus/${menuId}`,
    params,
    showSuccessMessage: true
  })
}

export function fetchDeleteMenu(menuId: number) {
  return request.del<void>({
    url: `/api/v1/system/menus/${menuId}`,
    showSuccessMessage: true
  })
}

export function fetchCreateMenuButton(params: Record<string, any>) {
  return request.post<{ id: number }>({
    url: '/api/v1/system/menu-buttons',
    params,
    showSuccessMessage: true
  })
}

export function fetchUpdateMenuButton(buttonId: number, params: Record<string, any>) {
  return request.put<{ id: number }>({
    url: `/api/v1/system/menu-buttons/${buttonId}`,
    params,
    showSuccessMessage: true
  })
}

export function fetchDeleteMenuButton(buttonId: number) {
  return request.del<void>({
    url: `/api/v1/system/menu-buttons/${buttonId}`,
    showSuccessMessage: true
  })
}

export function fetchGetInstalledPlugins() {
  return request.get<Api.System.InstalledPlugin[]>({
    url: '/api/v1/system/plugins'
  })
}

export function fetchGetSystemLogs(params: Api.System.SystemLogSearchParams) {
  return request.get<Api.System.SystemLogList>({
    url: '/api/v1/system/logs',
    params
  })
}

export function fetchGetSystemLogRuntime() {
  return request.get<Api.System.SystemLogRuntimeStatus>({
    url: '/api/v1/system/logs/runtime'
  })
}

export function fetchUpdateSystemLogRuntime(params: Api.System.SystemLogSettingsPayload) {
  return request.put<Api.System.SystemLogRuntimeStatus>({
    url: '/api/v1/system/logs/runtime',
    params,
    showSuccessMessage: true
  })
}
