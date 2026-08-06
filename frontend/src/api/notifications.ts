/** 前端接口封装：notifications。 */
import request from '@/utils/http'

export type InAppNoticePage = Api.Notifications.InAppNoticePage

export function fetchInAppNoticeList(params?: Api.Common.CursorParams) {
  return request.get<InAppNoticePage>({
    url: '/api/v1/notification-deliveries',
    params
  })
}

export function fetchReadInAppNotice(deliveryId: number) {
  return request.post<{ unreadCount: number }>({
    url: `/api/v1/notification-deliveries/${deliveryId}/read`,
    showSuccessMessage: false
  })
}

export function fetchReadAllInAppNotice() {
  return request.post<{ updatedCount: number }>({
    url: '/api/v1/notification-deliveries/read-all',
    showSuccessMessage: true
  })
}

export function fetchTestInAppNotice() {
  return request.post<{
    record: Api.Notifications.InAppNoticeItem
    unreadCount: number
  }>({
    url: '/api/v1/notification-deliveries/tests',
    showSuccessMessage: true
  })
}
