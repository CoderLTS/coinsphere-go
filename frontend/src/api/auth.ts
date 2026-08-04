/** 前端接口封装：auth。 */
import request from '@/utils/http'

/**
 * 用户登录。
 */
export function fetchLogin(params: Api.Auth.LoginParams) {
  return request.post<Api.Auth.LoginResponse>({
    url: '/api/auth/login',
    params,
    skipAuthRefresh: true
  })
}

/**
 * 获取当前登录用户信息。
 */
export function fetchGetUserInfo() {
  return request.get<Api.Auth.UserInfo>({
    url: '/api/auth/me'
  })
}

/**
 * 使用 HttpOnly Cookie 轮换刷新会话，调用方无需接触 refresh token。
 */
export function fetchRefreshSession() {
  return request.post<Api.Auth.LoginResponse>({
    url: '/api/auth/refresh',
    skipAuthRefresh: true,
    showErrorMessage: false
  })
}

/** 退出登录由后端吊销 Cookie 中的 refresh token，失败不阻塞本地清理。 */
export function logout() {
  return request.post<null>({
    url: '/api/auth/logout',
    skipAuthRefresh: true,
    showErrorMessage: false
  })
}
