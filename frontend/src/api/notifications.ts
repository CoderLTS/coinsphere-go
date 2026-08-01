/** 前端接口封装：notifications。 */
import request from '@/utils/http'

export type InAppNoticePage = Api.Notifications.InAppNoticePage

export function fetchInAppNoticeList(params?: { current?: number; size?: number }) {
  return request.get<InAppNoticePage>({
    url: '/api/notifications/in-app',
    params
  })
}

export function fetchReadInAppNotice(deliveryId: number) {
  return request.post<{ unreadCount: number }>({
    url: `/api/notifications/in-app/${deliveryId}/read`,
    showSuccessMessage: false
  })
}

export function fetchReadAllInAppNotice() {
  return request.post<{ updatedCount: number }>({
    url: '/api/notifications/in-app/read-all',
    showSuccessMessage: true
  })
}

export function fetchTestInAppNotice() {
  return request.post<{
    record: Api.Notifications.InAppNoticeItem
    unreadCount: number
  }>({
    url: '/api/notifications/in-app/tests',
    showSuccessMessage: true
  })
}
