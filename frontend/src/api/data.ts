/** 前端接口封装：data。 */
import request from '@/utils/http'

export function fetchNewsList(params: Api.Data.NewsSearchParams) {
  return request.get<Api.Data.NewsList>({
    url: '/api/v1/data/news',
    params
  })
}

export function fetchCreateNews(params: Api.Data.NewsUpsertPayload) {
  return request.post<Api.Data.NewsListItem>({
    url: '/api/v1/data/news',
    params,
    showSuccessMessage: true
  })
}

export function fetchUpdateNews(newsId: number, params: Api.Data.NewsUpsertPayload) {
  return request.put<Api.Data.NewsListItem>({
    url: `/api/v1/data/news/${newsId}`,
    params,
    showSuccessMessage: true
  })
}

export function fetchDeleteNews(newsId: number) {
  return request.del<void>({
    url: `/api/v1/data/news/${newsId}`,
    showSuccessMessage: true
  })
}
