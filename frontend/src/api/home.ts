/** 前端接口封装：home。 */
import request from '@/utils/http'

export interface HomeOverview {
  process: {
    uptimeSeconds: number
    goMemoryAllocBytes: number
    goMemorySysBytes: number
    goroutines: number
  }
  http: {
    requestsTotal: number
    requestsFailed: number
    requestsInFlight: number
    trend: Array<{ time: string; requests: number; failed: number; averageLatencyMs: number }>
  }
  database: {
    status: 'healthy' | 'unavailable' | string
    schemaVersion: number
    maxOpenConnections: number
    openConnections: number
    inUse: number
    idle: number
    waitCount: number
  }
}

export function fetchHomeOverview() {
  return request.get<HomeOverview>({
    url: '/api/v1/home/overview'
  })
}

export function fetchHomeMeta() {
  return request.get<{ service: string; version: string; sdkMajor: number; pluginCount: number }>({
    url: '/api/v1/home/meta'
  })
}
