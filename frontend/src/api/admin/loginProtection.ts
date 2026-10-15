import { apiClient } from '../client'
import type { PaginatedResponse } from '@/types'

export interface LoginProtectionSettings {
  enabled: boolean
  window_minutes: number
  max_failures: number
  ban_hours: number
}

export interface LoginIPBan {
  ip: string
  blocked_at: string
  expires_at: string
}

const base = '/admin/settings/login-protection'

export async function getSettings(): Promise<LoginProtectionSettings> {
  return (await apiClient.get<LoginProtectionSettings>(base)).data
}

export async function saveSettings(settings: LoginProtectionSettings): Promise<LoginProtectionSettings> {
  return (await apiClient.put<LoginProtectionSettings>(base, settings)).data
}

export async function listBans(page: number, pageSize: number): Promise<PaginatedResponse<LoginIPBan>> {
  return (await apiClient.get<PaginatedResponse<LoginIPBan>>(`${base}/bans`, { params: { page, page_size: pageSize } })).data
}

export async function unban(ip: string): Promise<void> {
  await apiClient.post(`${base}/unban`, { ip })
}
