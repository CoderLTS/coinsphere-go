/** 前端接口封装：auth。 */
import request from '@/utils/http'

/**
 * 用户登录。
 */
export function fetchLogin(params: Api.Auth.LoginParams) {
  return request.post<Api.Auth.LoginResponse>({
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
  return request.post<Api.Auth.ReauthResponse>({
    url: '/api/v1/auth/reauth',
    params: { password },
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
