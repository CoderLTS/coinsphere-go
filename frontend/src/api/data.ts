/** 前端接口封装：data。 */
import request from '@/utils/http'

export interface NotifyDeliverySearchParams {
  current: number
  size: number
  keyword?: string
  workflowDefinitionId?: number
  channelType?: Api.Config.NotifyChannelType | string
  deliveryStatus?: 'pending' | 'success' | 'failed' | 'skipped_offline' | string
}

export interface NotifyDeliveryItem {
  id: number
  workflowExecutionId?: number | null
  workflowExecutionNodeId?: number | null
  workflowDefinitionId?: number | null
  workflowDefinitionCode: string
  workflowDefinitionName: string
  targetType: string
  targetId?: number | null
  targetLabel: string
  recipientId?: number | null
  recipientLabel: string
  channelType: string
  channelTypeLabel: string
  channelDisplayName: string
  deliveryStatus: string
  deliveryStatusLabel: string
  messageTitle: string
  messageContent: string
  providerResponseText: string
  errorMessage: string
  isRead: boolean
  readAt: string
  sentAt: string
  createdAt: string
}

export interface NotifyDeliveryList extends Api.Common.PaginatedResponse<NotifyDeliveryItem> {}

export function fetchNewsList(params: Api.Data.NewsSearchParams) {
  return request.get<Api.Data.NewsList>({
    url: '/api/data/news',
    params
  })
}

export function fetchCreateNews(params: Api.Data.NewsUpsertPayload) {
  return request.post<Api.Data.NewsListItem>({
    url: '/api/data/news',
    params,
    showSuccessMessage: true
  })
}

export function fetchUpdateNews(newsId: number, params: Api.Data.NewsUpsertPayload) {
  return request.put<Api.Data.NewsListItem>({
    url: `/api/data/news/${newsId}`,
    params,
    showSuccessMessage: true
  })
}

export function fetchDeleteNews(newsId: number) {
  return request.del<void>({
    url: `/api/data/news/${newsId}`,
    showSuccessMessage: true
  })
}

export function fetchPushDeliveryList(params: NotifyDeliverySearchParams) {
  return request.get<NotifyDeliveryList>({
    url: '/api/data/push-deliveries',
    params: {
      current: params.current,
      size: params.size,
      keyword: params.keyword,
      workflowDefinitionId: params.workflowDefinitionId,
      channelType: params.channelType,
      deliveryStatus: params.deliveryStatus
    }
  })
}
