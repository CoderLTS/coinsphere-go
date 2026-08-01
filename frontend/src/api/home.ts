/** 前端接口封装：home。 */
import request from '@/utils/http'

export interface HomeRecentNewsItem {
  id: number
  sourceMessageId: number
  title: string
  summary: string
  publishedAt: string
}

export interface HomeDefinitionItem {
  workflowDefinitionId: number
  workflowDefinitionCode: string
  workflowDefinitionName: string
  isActive: boolean
  runCount: number
  createdAt: string
}

export interface HomeOverview {
  stats: {
    newsTotal: number
    newsToday: number
    activeDefinitions: number
  }
  recentNews: HomeRecentNewsItem[]
  definitions: HomeDefinitionItem[]
}

export function fetchHomeOverview() {
  return request.get<HomeOverview>({
    url: '/api/home/overview'
  })
}

export function fetchHomeMeta() {
  return request.get<{ service: string; version: string }>({
    url: '/api/home/meta'
  })
}
