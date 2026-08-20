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
    maxOpenConnections: number
    openConnections: number
    inUse: number
    idle: number
    waitCount: number
  }
  workers: Array<{
    lane: 'realtime' | 'backtest' | string
    status: 'online' | 'offline' | string
    workerId: string
    lastHeartbeatAt: string
    queuedCount: number
    activeCount: number
  }>
  workflow: {
    activeDefinitions: number
    runningCount: number
    failedCount: number
    successCount: number
  }
  market: {
    status: string
    lastSyncAt: string
    nextSyncAt: string
    instrumentCount: number
  }
  trading: {
    accountCount: number
    activeAccountCount: number
    pausedAccountCount: number
    emergencyStopped: boolean
  }
  alerts: Array<{
    severity: string
    title: string
    description: string
    count?: number
    path?: string
  }>
}

export function fetchHomeOverview() {
  return request.get<HomeOverview>({
    url: '/api/v1/home/overview'
  })
}

export function fetchHomeMeta() {
  return request.get<{ service: string; version: string }>({
    url: '/api/v1/home/meta'
  })
}
