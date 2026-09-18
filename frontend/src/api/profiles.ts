import request from '@/utils/http'
import type { ProfileRef, ProfileSummary } from './workflows'

export interface ProfileVersion {
  version: string
  status: 'draft' | 'published' | 'disabled' | string
  config?: Record<string, unknown>
  summary?: string
  publishedAt?: string
  updatedAt?: string
}

export interface ProfileDescriptor {
  type: string
  title: string
  description: string
  configSchema: Record<string, unknown>
  uiSchema: Record<string, unknown>
}

export interface ProfileRecord extends ProfileSummary {
  versions: ProfileVersion[]
  descriptor?: ProfileDescriptor
}

interface ProfileList { items: ProfileRecord[] }
const profileBase = (pluginId: string, type?: string) => `/api/v1/plugins/${encodeURIComponent(pluginId)}/profiles${type ? `/${encodeURIComponent(type)}` : ''}`

export const fetchProfileDescriptor = (pluginId: string, type: string) =>
  request.get<{ item: ProfileDescriptor }>({ url: `${profileBase(pluginId, type)}/descriptor` })

export const fetchProfiles = (pluginId: string, type?: string) =>
  request.get<ProfileList>({ url: profileBase(pluginId, type), params: {} })

export const fetchProfile = (pluginId: string, type: string, profileId: string) =>
  request.get<{ item: ProfileRecord }>({ url: `${profileBase(pluginId, type)}/${encodeURIComponent(profileId)}` })

export const saveProfile = (pluginId: string, profileId: string | undefined, payload: {
  name: string; summary: string; type: string; config: Record<string, unknown>; expectedVersion?: string
}) => request.request<ProfileRecord>({
  url: `${profileBase(pluginId, payload.type)}${profileId ? `/${encodeURIComponent(profileId)}` : ''}`,
  method: profileId ? 'PUT' : 'POST', data: payload
})

export const copyProfile = (pluginId: string, type: string, profileId: string, name?: string) =>
  request.post<ProfileRecord>({ url: `${profileBase(pluginId, type)}/${encodeURIComponent(profileId)}/copy`, data: name ? { name } : {} })

export const publishProfile = (pluginId: string, type: string, profileId: string, version: string) =>
  request.post<ProfileRecord>({ url: `${profileBase(pluginId, type)}/${encodeURIComponent(profileId)}/publish`, data: { version } })

export const disableProfile = (pluginId: string, type: string, profileId: string) =>
  request.post<ProfileRecord>({ url: `${profileBase(pluginId, type)}/${encodeURIComponent(profileId)}/disable` })

export const profileRef = (profile: ProfileSummary, version?: string): ProfileRef => ({
  pluginId: profile.pluginId,
  profileId: profile.id,
  version: version || profile.latestPublishedVersion || profile.version || '',
  type: profile.type
})
