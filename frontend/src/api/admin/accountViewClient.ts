import type { AxiosRequestConfig, AxiosResponse } from 'axios'
import { apiClient } from '../client'
import type { CapturedAccountView } from '@/composables/useAccountViewContext'

export interface AccountViewRequestOptions { view?: CapturedAccountView; signal?: AbortSignal; etag?: string | null }
export interface AccountHTTPClient {
  request<T = unknown>(config: AxiosRequestConfig): Promise<AxiosResponse<T>>
  get<T = unknown>(url: string, config?: AxiosRequestConfig): Promise<AxiosResponse<T>>
  post<T = unknown>(url: string, data?: unknown, config?: AxiosRequestConfig): Promise<AxiosResponse<T>>
  put<T = unknown>(url: string, data?: unknown, config?: AxiosRequestConfig): Promise<AxiosResponse<T>>
  delete<T = unknown>(url: string, config?: AxiosRequestConfig): Promise<AxiosResponse<T>>
}

/** Streaming callers share the same native read guard without adding view metadata. */
export function accountViewRequestConfig(scope: CapturedAccountView, config: AxiosRequestConfig): AxiosRequestConfig {
  scope.assertCurrent()
  return config
}

/** Per-call client only: no route/global Axios interceptor or mutable authority. */
export function accountViewClient(scope?: CapturedAccountView): AccountHTTPClient {
  if (!scope) return apiClient
  const request = async <T = unknown>(config: AxiosRequestConfig): Promise<AxiosResponse<T>> => {
    scope.assertCurrent()
    const response = await apiClient.request<T>(config)
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
