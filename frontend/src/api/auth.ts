/** 前端接口封装：auth。 */
import request from '@/utils/http'

/**
 * 用户登录。
 */
export function fetchLogin(params: Api.Auth.LoginParams) {
  return request.post<Api.Auth.LoginResponse>({
    url: '/api/auth/login',
    params
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
 * 退出登录：请求后端吊销该 refresh 令牌(端到端登出，见评审 #4)。
 * showErrorMessage:false —— 登出属 best-effort，失败不弹错误提示。
 */
export function logout(refreshToken: string) {
  return request.post<null>({
    url: '/api/auth/logout',
    params: { refreshToken },
    showErrorMessage: false
  })
}
