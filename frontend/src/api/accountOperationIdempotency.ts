import type { InternalAxiosRequestConfig } from 'axios'

// Keep only unresolved submissions. A timeout may have happened after the
// server committed the operation; retrying that payload must reuse its key.
const unresolved = new Map<string, string>()
let session = 0
type OperationConfig = InternalAxiosRequestConfig & { accountOperationSignature?: string }
export async function stabilizeAccountOperationKey(config: OperationConfig): Promise<void> {
  const url = String(config.url || '')
  const key = config.headers?.get('Idempotency-Key')
  if (!key || !/^\/admin\/(accounts(?:\/|$)|account-jobs(?:\/|$))/.test(url)) return
  const epoch = session
  const payload = `${config.method}:${url}:${typeof config.data === 'string' ? config.data : JSON.stringify(config.data)}`
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(payload))
  if (epoch !== session) throw new Error('Account operation session changed')
  const signature = `${epoch}:` + Array.from(new Uint8Array(digest), n => n.toString(16).padStart(2, '0')).join('')
  if (!unresolved.has(signature)) unresolved.set(signature, String(key))
  config.headers.set('Idempotency-Key', unresolved.get(signature)!)
  config.accountOperationSignature = signature
}
export function settleAccountOperation(config?: OperationConfig): void {
  if (config?.accountOperationSignature) unresolved.delete(config.accountOperationSignature)
}
export function clearAccountOperationKeys(): void { unresolved.clear(); session++ }
