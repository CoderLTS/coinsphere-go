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
