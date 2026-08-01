/** 前端接口封装：system。 */
import request from '@/utils/http'
import { AppRouteRecord } from '@/types/router'

export function fetchGetUserList(params: Api.System.UserSearchParams) {
  return request.get<Api.System.UserList>({
    url: '/api/system/users',
    params
  })
}

export function fetchGetRoleList(params: Api.System.RoleSearchParams) {
  return request.get<Api.System.RoleList>({
    url: '/api/system/roles',
    params
  })
}

export function fetchCreateRole(params: Api.System.RoleUpsertPayload) {
  return request.post<Api.System.RoleListItem>({
    url: '/api/system/roles',
    params,
    showSuccessMessage: true
  })
}

export function fetchUpdateRole(roleId: number, params: Api.System.RoleUpsertPayload) {
  return request.put<Api.System.RoleListItem>({
    url: `/api/system/roles/${roleId}`,
    params,
    showSuccessMessage: true
  })
}

export function fetchDeleteRole(roleId: number) {
  return request.del<void>({
    url: `/api/system/roles/${roleId}`,
    showSuccessMessage: true
  })
}

export function fetchSaveRolePermissions(roleId: number, params: Api.System.RolePermissionPayload) {
  return request.put<void>({
    url: `/api/system/roles/${roleId}/permissions`,
    params,
    showSuccessMessage: true
  })
}

export function fetchGetMenuList() {
  return request.get<AppRouteRecord[]>({
    url: '/api/system/menus'
  })
}

export function fetchGetManageMenuTree() {
  return request.get<AppRouteRecord[]>({
    url: '/api/system/menus/manage-tree'
  })
}

export function fetchGetMenuI18nDict() {
  return request.get<Api.System.MenuI18nDict>({
    url: '/api/system/i18n-dictionaries',
    params: { scope: 'menu' }
  })
}

export function fetchCreateUser(params: Record<string, any>) {
  return request.post<Api.System.UserListItem>({
    url: '/api/system/users',
    params,
    showSuccessMessage: true
  })
}

export function fetchUpdateUser(userId: number, params: Record<string, any>) {
  return request.put<Api.System.UserListItem>({
    url: `/api/system/users/${userId}`,
    params,
    showSuccessMessage: true
  })
}

export function fetchDeleteUser(userId: number) {
  return request.del<void>({
    url: `/api/system/users/${userId}`,
    showSuccessMessage: true
  })
}

export function fetchUploadAvatarAsset(file: File) {
  const formData = new FormData()
  formData.append('avatar', file)
  return request.post<{ url: string }>({
    url: '/api/system/uploads/avatars',
    data: formData,
    showSuccessMessage: true
  })
}

export function fetchUploadUserAvatar(file: File) {
  return fetchUploadAvatarAsset(file)
}

export function fetchCreateMenu(params: Record<string, any>) {
  return request.post<{ id: number }>({
    url: '/api/system/menus',
    params,
    showSuccessMessage: true
  })
}

export function fetchUpdateMenu(menuId: number, params: Record<string, any>) {
  return request.put<{ id: number }>({
    url: `/api/system/menus/${menuId}`,
    params,
    showSuccessMessage: true
  })
}

export function fetchDeleteMenu(menuId: number) {
  return request.del<void>({
    url: `/api/system/menus/${menuId}`,
    showSuccessMessage: true
  })
}

export function fetchCreateMenuButton(params: Record<string, any>) {
  return request.post<{ id: number }>({
    url: '/api/system/menu-buttons',
    params,
    showSuccessMessage: true
  })
}

export function fetchUpdateMenuButton(buttonId: number, params: Record<string, any>) {
  return request.put<{ id: number }>({
    url: `/api/system/menu-buttons/${buttonId}`,
    params,
    showSuccessMessage: true
  })
}

export function fetchDeleteMenuButton(buttonId: number) {
  return request.del<void>({
    url: `/api/system/menu-buttons/${buttonId}`,
    showSuccessMessage: true
  })
}
