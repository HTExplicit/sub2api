import type { AxiosRequestConfig, AxiosResponse } from 'axios'
import { apiClient } from '../client'
import type { CapturedAccountView } from '@/composables/useAccountViewContext'
import { ACCOUNT_VIEW_HEADER, accountViewIdentityHeader, cloneViewContext } from '@/components/plugins/accountView'

export interface AccountViewRequestOptions { view?: CapturedAccountView; signal?: AbortSignal; etag?: string | null }
export interface AccountHTTPClient {
  request<T = unknown>(config: AxiosRequestConfig): Promise<AxiosResponse<T>>
  get<T = unknown>(url: string, config?: AxiosRequestConfig): Promise<AxiosResponse<T>>
  post<T = unknown>(url: string, data?: unknown, config?: AxiosRequestConfig): Promise<AxiosResponse<T>>
  put<T = unknown>(url: string, data?: unknown, config?: AxiosRequestConfig): Promise<AxiosResponse<T>>
  delete<T = unknown>(url: string, config?: AxiosRequestConfig): Promise<AxiosResponse<T>>
}

export function accountViewRequestConfig(scope: CapturedAccountView, config: AxiosRequestConfig): AxiosRequestConfig {
  scope.assertCurrent()
  const { query: _query, ...identity } = scope.context
  const method = String(config.method || 'GET').toUpperCase()
  let data = config.data
  if (!['GET', 'HEAD'].includes(method)) {
    if (data instanceof FormData || (data !== undefined && data !== null && (typeof data !== 'object' || Array.isArray(data)))) {
      throw new Error('ACCOUNT_VIEW_INVALID_REQUEST')
    }
    // The captured host context is authoritative, never an iframe/body override.
    data = { ...(data || {}), view_context: cloneViewContext(scope.context) }
  }
  return { ...config, data, headers: { ...config.headers, [ACCOUNT_VIEW_HEADER]: accountViewIdentityHeader(identity) } }
}

/** Per-call client only: no route/global Axios interceptor or mutable authority. */
export function accountViewClient(scope?: CapturedAccountView): AccountHTTPClient {
  if (!scope) return apiClient
  const request = async <T = unknown>(config: AxiosRequestConfig): Promise<AxiosResponse<T>> => {
    const response = await apiClient.request<T>(accountViewRequestConfig(scope, config))
    scope.assertCurrent()
    return response
  }
  return {
    request,
    get: <T = unknown>(url: string, config?: AxiosRequestConfig) => request<T>({ ...config, url, method: 'GET' }),
    post: <T = unknown>(url: string, data?: unknown, config?: AxiosRequestConfig) => request<T>({ ...config, url, data, method: 'POST' }),
    put: <T = unknown>(url: string, data?: unknown, config?: AxiosRequestConfig) => request<T>({ ...config, url, data, method: 'PUT' }),
    delete: <T = unknown>(url: string, config?: AxiosRequestConfig) => request<T>({ ...config, url, method: 'DELETE' })
  }
}
