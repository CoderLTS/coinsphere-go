/** 前端接口封装：auth。 */
import request from '@/utils/http'
import type { components } from '@/types/generated/openapi'

type LoginRequest = components['schemas']['LoginRequest']
type LoginData = components['schemas']['LoginData']
type ReauthRequest = components['schemas']['ReauthRequest']
type ReauthData = components['schemas']['ReauthData']

/**
 * 用户登录。
 */
export function fetchLogin(params: LoginRequest) {
  return request.post<LoginData>({
    url: '/api/v1/auth/login',
    params
  })
}

/**
 * 获取当前登录用户信息。
 */
export function fetchGetUserInfo() {
  return request.get<Api.Auth.UserInfo>({
    url: '/api/v1/me'
  })
}

export function fetchReauth(password: string) {
  const params: ReauthRequest = { password }
  return request.post<ReauthData>({
    url: '/api/v1/auth/reauth',
    params,
    showErrorMessage: false
  })
}

/** 退出登录失败不阻塞本地会话清理。 */
export function logout(accessToken: string) {
  return request.post<null>({
    url: '/api/v1/auth/logout',
    headers: { Authorization: `Bearer ${accessToken}` },
    showErrorMessage: false
  })
}
