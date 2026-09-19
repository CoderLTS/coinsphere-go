import request from '@/utils/http'

export interface Connection {
  id: string
  name: string
  type: string
  version: number
  enabled: boolean
  config: Record<string, any>
  secretFields: Record<string, boolean>
}
export interface ConnectionType {
  type: string
  schema: Record<string, any>
}
export const fetchConnections = () =>
  request.get<{ items: Connection[] }>({ url: '/api/v1/connections' })
export const fetchConnectionTypes = () =>
  request.get<{ items: ConnectionType[] }>({ url: '/api/v1/connections/types' })
export const saveConnection = (
  id: string,
  data: {
    name: string
    type: string
    config: Record<string, any>
    secrets: Record<string, string>
    enabled: boolean
    expectedVersion: number
  }
) =>
  request.request<Connection>({
    url: `/api/v1/connections${id ? `/${id}` : ''}`,
    method: id ? 'PUT' : 'POST',
    data
  })
export const deleteConnection = (id: string) => request.del({ url: `/api/v1/connections/${id}` })
