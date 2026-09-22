import type { ResourceInput } from '@sub2api/plugin-ui/client'
import { callLocalResource } from './localResources'
import { pluginDispatchClient, pluginDispatchHeaders, type PluginDispatchContext } from '@/api/admin/plugins'

export interface PluginResourceDescriptor {
  name: string
  capability: string
  permission: 'admin' | 'user'
  method: string
  path: string
  available: boolean
  response_kind?: 'blob'
  retained?: boolean
}

export function resourceRequest(descriptor: PluginResourceDescriptor, input: ResourceInput) {
  if (!descriptor.available || !['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD'].includes(descriptor.method) ||
      !descriptor.path.startsWith('/api/v1/') || /[\\?#]/.test(descriptor.path) || descriptor.path.includes('..')) throw new Error('Plugin resource unavailable')
  const used = new Set<string>()
  const url = descriptor.path.replace(/:([a-zA-Z_][a-zA-Z0-9_]*)/g, (_, key: string) => {
    used.add(key)
    const value = input.params?.[key]
    if ((typeof value !== 'string' && typeof value !== 'number') || !/^[a-zA-Z0-9_-]{1,200}$/.test(String(value))) throw new Error('Invalid resource identifier')
    return encodeURIComponent(String(value))
  })
  if (Object.keys(input.params || {}).some(key => !used.has(key))) throw new Error('Unexpected resource identifier')
  let data = input.body
  if (input.form) {
    if (!Array.isArray(input.form) || input.form.length > 64 || data !== undefined) throw new Error('Invalid resource form')
    const form = new FormData()
    for (const item of input.form) {
      if (!Array.isArray(item) || item.length !== 2 || !/^[a-z][a-z0-9_]{0,63}$/.test(item[0]) ||
          (typeof item[1] !== 'string' && !(item[1] instanceof Blob))) throw new Error('Invalid resource form field')
      form.append(item[0], item[1])
    }
    data = form
  }
  if ((descriptor.method === 'GET' || descriptor.method === 'HEAD') && data !== undefined) throw new Error('Read resource cannot carry a body')
  return { url, method: descriptor.method, data, params: input.query }
}

export async function callPluginResource(pluginID: number, packageSHA: string, descriptor: PluginResourceDescriptor, input: ResourceInput, signal?: AbortSignal, actorID?: number, dispatch?: PluginDispatchContext) {
  if (descriptor.method === 'LOCAL') {
    if (!descriptor.available || descriptor.permission !== 'user' || !actorID || input.body !== undefined || input.form !== undefined) throw new Error('Local resource unavailable')
    return callLocalResource(descriptor.name, actorID, input.local_data)
  }
  if (input.local_data !== undefined) throw new Error('Browser-local data cannot be sent to an HTTP resource')
  const request = resourceRequest(descriptor, input)
  if (input.operation_key !== undefined && !/^[a-zA-Z0-9:_-]{1,160}$/.test(input.operation_key)) throw new Error('Invalid operation identity')
  const { data } = await pluginDispatchClient(dispatch).request({ ...request, signal, baseURL: '', responseType: descriptor.response_kind === 'blob' ? 'blob' : 'json', headers: {
    'X-Sub2API-Plugin': String(pluginID), 'X-Sub2API-Plugin-Package': packageSHA,
    ...pluginDispatchHeaders(dispatch),
    ...(input.operation_key ? { 'Idempotency-Key': input.operation_key } : {}),
    ...(request.data instanceof FormData ? { 'Content-Type': 'multipart/form-data' } : {})
  } })
  return data
}
