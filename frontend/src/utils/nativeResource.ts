import { apiClient } from '@/api/client'
import { callLocalResource } from '@/utils/imageStudioLocalResources'
import { useAuthStore } from '@/stores/auth'

// Same input shape the former plugin pages used with the bridge `resource()`,
// so moved pages keep their API modules unchanged.
export interface ResourceInput {
  local_data?: unknown
  operation_key?: string
  params?: Record<string, string | number>
  query?: Record<string, unknown>
  body?: unknown
  form?: Array<[string, string | Blob]>
}

type NativeResource =
  | { method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'; path: string; blob?: boolean }
  | { method: 'LOCAL' }

// Former plugin resource names mapped to their host REST routes (relative to the API base).
const nativeResources: Record<string, NativeResource> = {
  'cindy.probe.list': { method: 'GET', path: '/admin/cindy/balance-probe-jobs' },
  'cindy.probe.preview': { method: 'POST', path: '/admin/cindy/balance-probe-jobs/preview' },
  'cindy.probe.create': { method: 'POST', path: '/admin/cindy/balance-probe-jobs' },
  'cindy.probe.get': { method: 'GET', path: '/admin/cindy/balance-probe-jobs/:id' },
  'cindy.probe.items': { method: 'GET', path: '/admin/cindy/balance-probe-jobs/:id/items' },
  'cindy.probe.rate': { method: 'PATCH', path: '/admin/cindy/balance-probe-jobs/:id/rate' },
  'cindy.probe.pause': { method: 'POST', path: '/admin/cindy/balance-probe-jobs/:id/pause' },
  'cindy.probe.resume': { method: 'POST', path: '/admin/cindy/balance-probe-jobs/:id/resume' },
  'cindy.probe.cancel': { method: 'POST', path: '/admin/cindy/balance-probe-jobs/:id/cancel' },
  'cindy.duplicates': { method: 'GET', path: '/admin/accounts/cindy/duplicate-identity-inventory' },
  'cindy.groups.audit': { method: 'GET', path: '/admin/cindy/groups/audit' },
  'cindy.groups.keys': { method: 'GET', path: '/admin/cindy/groups/:id/keys' },
  'cindy.groups.preview': { method: 'POST', path: '/admin/cindy/groups/:id/split-preview' },
  'cindy.groups.split': { method: 'POST', path: '/admin/cindy/groups/:id/split' },
  'cindy.cleanup.insufficient.preview': { method: 'GET', path: '/admin/accounts/cindy/insufficient-delete-preview' },
  'cindy.cleanup.insufficient.submit': { method: 'POST', path: '/admin/accounts/cindy/delete-insufficient' },
  'cindy.cleanup.banned.preview': { method: 'GET', path: '/admin/accounts/cindy/banned-delete-preview' },
  'cindy.cleanup.banned.submit': { method: 'POST', path: '/admin/accounts/cindy/delete-banned' },
  'image.keys': { method: 'GET', path: '/image-studio/eligible-keys' },
  'image.create': { method: 'POST', path: '/image-studio/jobs' },
  'image.jobs': { method: 'GET', path: '/image-studio/jobs' },
  'image.job': { method: 'GET', path: '/image-studio/jobs/:id' },
  'image.items': { method: 'GET', path: '/image-studio/jobs/:id/items' },
  'image.cancel': { method: 'POST', path: '/image-studio/jobs/:id/cancel' },
  'image.retry': { method: 'POST', path: '/image-studio/jobs/:id/retry' },
  'image.artifact': { method: 'GET', path: '/image-studio/jobs/:id/artifacts/:artifact_id', blob: true },
  'image.history.list': { method: 'LOCAL' },
  'image.history.save': { method: 'LOCAL' },
  'image.history.delete': { method: 'LOCAL' },
  'image.history.clear': { method: 'LOCAL' }
}

function resourceURL(path: string, params: Record<string, string | number> = {}): string {
  const used = new Set<string>()
  const url = path.replace(/:([a-zA-Z_][a-zA-Z0-9_]*)/g, (_, key: string) => {
    used.add(key)
    const value = params[key]
    if ((typeof value !== 'string' && typeof value !== 'number') || !/^[a-zA-Z0-9_-]{1,200}$/.test(String(value))) {
      throw new Error('Invalid resource identifier')
    }
    return encodeURIComponent(String(value))
  })
  if (Object.keys(params).some(key => !used.has(key))) throw new Error('Unexpected resource identifier')
  return url
}

export async function resource<T>(name: string, input: ResourceInput = {}, signal?: AbortSignal): Promise<T> {
  const descriptor = nativeResources[name]
  if (!descriptor) throw new Error('Unknown resource')
  if (descriptor.method === 'LOCAL') {
    return await callLocalResource(name, Number(useAuthStore().user?.id), input.local_data) as T
  }
  if (input.local_data !== undefined) throw new Error('Browser-local data cannot be sent to an HTTP resource')
  let data: unknown = input.body
  if (input.form) {
    if (data !== undefined) throw new Error('Invalid resource form')
    const form = new FormData()
    for (const [key, value] of input.form) form.append(key, value)
    data = form
  }
  if (descriptor.method === 'GET' && data !== undefined) throw new Error('Read resource cannot carry a body')
  const response = await apiClient.request({
    url: resourceURL(descriptor.path, input.params),
    method: descriptor.method,
    data,
    params: input.query,
    signal,
    responseType: descriptor.blob ? 'blob' : 'json',
    headers: {
      ...(input.operation_key ? { 'Idempotency-Key': input.operation_key } : {}),
      ...(data instanceof FormData ? { 'Content-Type': 'multipart/form-data' } : {})
    }
  })
  return response.data as T
}
