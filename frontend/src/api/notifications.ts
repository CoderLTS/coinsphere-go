/** 前端接口封装：notifications。 */
import request from '@/utils/http'

export type InAppNoticePage = Api.Notifications.InAppNoticePage

export interface NotificationDelivery {
  id: number
  channel: 'in_app' | 'dingtalk' | 'qq' | 'smtp'
  recipientUserId?: number | null
  subjectKey: string
  title: string
  message: string
  status: 'pending' | 'delivered' | 'failed'
  attemptCount: number
  lastErrorCategory?: string | null
  deliveredAt?: string | null
  createdAt: string
}

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

export function fetchNotificationDeliveries() {
  return request.get<{ items: NotificationDelivery[] }>({
    url: '/api/v1/plugins/official.notification/deliveries'
  })
}
