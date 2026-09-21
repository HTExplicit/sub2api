import type { InternalAxiosRequestConfig } from 'axios'

// Keep only unresolved submissions. A timeout may have happened after the
// server committed the operation; retrying that payload must reuse its key.
const unresolved = new Map<string, string>()
let session = 0
type OperationConfig = InternalAxiosRequestConfig & { accountOperationSignature?: string }

function currentAccountOperationActor(): number | null {
  try {
    const raw = globalThis.localStorage?.getItem('auth_user')
    if (!raw) return null
    const user: unknown = JSON.parse(raw)
    if (!user || typeof user !== 'object' || Array.isArray(user)) return null
    const id = (user as { id?: unknown }).id
    return typeof id === 'number' && Number.isSafeInteger(id) && id > 0 ? id : null
  } catch {
    return null
  }
}

export async function stabilizeAccountOperationKey(config: OperationConfig): Promise<void> {
  const url = String(config.url || '')
  const key = config.headers?.get('Idempotency-Key')
  if (!key || !/^\/(?:api\/v1\/)?admin\/(accounts(?:\/|$)|account-jobs(?:\/|$))/.test(url)) return
  const actor = currentAccountOperationActor()
  if (actor === null) return
  const epoch = session
  // 仅用于浏览器内重试键分区；服务端权限仍由真实认证和插件绑定校验决定。
  const payload = JSON.stringify([
    actor, config.method, url, config.baseURL ?? '',
    config.headers.get('X-Sub2API-Plugin') ?? null,
    config.headers.get('X-Sub2API-Plugin-Package') ?? null,
    typeof config.data === 'string' ? config.data : JSON.stringify(config.data),
  ])
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(payload))
  if (epoch !== session || currentAccountOperationActor() !== actor) throw new Error('Account operation session changed')
  const signature = `${epoch}:` + Array.from(new Uint8Array(digest), n => n.toString(16).padStart(2, '0')).join('')
  if (!unresolved.has(signature)) unresolved.set(signature, String(key))
  config.headers.set('Idempotency-Key', unresolved.get(signature)!)
  config.accountOperationSignature = signature
}
export function settleAccountOperation(config?: OperationConfig): void {
  if (config?.accountOperationSignature) unresolved.delete(config.accountOperationSignature)
}
export function clearAccountOperationKeys(): void { unresolved.clear(); session++ }
