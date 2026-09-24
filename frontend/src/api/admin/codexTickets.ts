import { apiClient } from '../client'
import { accountJobIdempotencyHeaders, type AccountJob } from './accountJobs'

export interface TicketResult {
  stage?: string
  code: string; success: boolean; message?: string; http_status?: number; observed_length?: number
  duration_ms?: number; expires_at?: string; fingerprint?: string
}
export interface ProxyTestResult {
  failure_detail?: string
  success: boolean; network_reachable: boolean; protocol: string; http_status?: number; code: string; message: string
  stages: Array<{ name: string; success: boolean; duration_ms: number; message?: string }>
  certificate_trust?: string; certificate_fingerprint?: string; protocol_suggestion?: string
}
export const codexTicketsAPI = {
  async policy() {
    return (await apiClient.get<{ enabled: boolean; models: string[] }>('/admin/accounts/codex-tickets/policy')).data
  },
  async harvest(ids: number[], models: string[], force: boolean, key = accountJobIdempotencyHeaders('codex_ticket_harvest')) {
    const path = ids.length === 1 ? `/admin/accounts/${ids[0]}/codex-tickets/harvest` : '/admin/accounts/codex-tickets/batch-harvest'
    return (await apiClient.post<AccountJob>(path, { account_ids: ids, models, force }, key)).data
  },
  async stop(id: number, models: string[]) {
    await apiClient.post(`/admin/accounts/${id}/codex-tickets/stop`, { models })
  },
  async stopJob(ids: number[], models: string[], key = accountJobIdempotencyHeaders('codex_ticket_stop')) {
    const path = ids.length === 1 ? `/admin/accounts/${ids[0]}/codex-tickets/stop-job` : '/admin/accounts/codex-tickets/batch-stop'
    return (await apiClient.post<AccountJob>(path, { account_ids: ids, models }, key)).data
  },
  async testProxy(proxy_url: string, protocol?: string) {
    const body = protocol ? { proxy_url, protocol } : { proxy_url }
    return (await apiClient.post<ProxyTestResult>('/admin/settings/openai-codex-ticket/proxy-test', body, { timeout: 30000 })).data
  }
}
