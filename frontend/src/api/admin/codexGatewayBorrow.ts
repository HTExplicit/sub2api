import { apiClient, buildApiUrl } from '../client'
import { ADMIN_UI_REQUEST_HEADER } from '../adminUIRequest'

export const BORROW_MODELS = ['gpt-6-astra', 'gpt-6.1-sol'] as const
export const PELICAN_BORROW_PROMPT = '创建一个HTML，内容是SVG绘制一个鹈鹕骑自行车的2D动画，你不需要任何测试'

export interface CodexGatewayBorrowConfig {
  enabled: boolean
  source_account_ids: number[]
  target_account_ids: number[]
  models: string[]
}

export interface BorrowSourceStatus {
  account_id: number
  state: string
  reason: string
  checked_at?: string
  expires_at?: string
  remaining_seconds: number
  error?: string
}

export interface BorrowAcquisition {
 trigger?: string; phase?: string; started_at?: string; finished_at?: string
 reason?: string; error?: string; retry_after?: string
}

export interface BorrowTargetStatus extends BorrowSourceStatus {
  acquisition?: BorrowAcquisition
  model: string
  cache_valid: boolean
  retry_after?: string
  mint_status: number
  continue_status: number
  minted: boolean
  new_ticket: boolean
  reported_model?: string
  request_shape?: string
  service_tier?: string
  mint_completed?: boolean
  continue_completed?: boolean
  mint_state_length?: number
  continue_state_length?: number
  route_changed?: boolean
}

export interface CodexGatewayBorrowUsage {
  account_id: number; model: string; transport: string; origin: string
  applied: boolean; reason: string; started_at: string; finished_at?: string
  outcome: string; request_id?: string; reported_model?: string; count: number; applied_count: number
  dispatched?: boolean; failure_stage?: string; client_request_id?: string; gateway_request_id?: string
  attempt_count?: number; blocked_count?: number
}
export interface CodexBorrowDiagnosticResult {
  scenario?: string; failure_stage?: string; failure_reason?: string; verification?: BorrowVerification
  tool_round_trip?: boolean; state_length?: number
  dispatched?: boolean
  read_error?: string
  mode: string; turn: number; applied: boolean; completed: boolean; response_id?: string
  reported_model?: string; answer: string; raw_response: string; error?: string; duration_ms: number
}
export interface CodexBorrowDiagnosticEvent {
  type: 'phase' | 'request' | 'result' | 'done' | 'error'; mode?: string; requests: number; limit: number
  result?: CodexBorrowDiagnosticResult; error?: string
}
export interface CodexBorrowDiagnosticRequest { account_id: number; model: string; transport: 'http' | 'ws'; request_limit?: number; scenario?: 'codex_session'; mode?: 'ordinary' | 'borrowed'; service_tier?: string }

export interface CodexGatewayBorrowStatus {
  acquisition?: BorrowAcquisition
  setup?: { state: string; phase: string; account_id: number; model: string; completed: number; total: number; failed: number; started_at: string; finished_at?: string; error?: string }
  recent_usage?: CodexGatewayBorrowUsage[]
  observed_since?: string
  enabled: boolean
  revision: number
  generated_at: string
  preparing: boolean
  config: CodexGatewayBorrowConfig
  candidate?: {
    source_account_id: number
    expires_at: string
    remaining_seconds: number
    cookie_fingerprint: string
  }
  sources: BorrowSourceStatus[]
  targets: BorrowTargetStatus[]
  model_efforts?: Record<string, string[]>
}

export interface BorrowVerification {
  account_id: number
  model: string
  success: boolean
  reason: string
  error?: string
  mint_status: number
  continue_status: number
  minted: boolean
  new_ticket: boolean
  reported_model?: string
  checked_at: string
  expires_at?: string
  request_shape?: string
  service_tier?: string
  mint_completed?: boolean
  continue_completed?: boolean
  mint_state_length?: number
  continue_state_length?: number
  route_changed?: boolean
}

export type BorrowTestResultStatus = 'pending' | 'running' | 'complete' | 'failed' | 'incomplete' | 'cancelled' | 'skipped'

export interface BorrowTestResult {
  id: string
  account_id: number
  account_name: string
  model_id: string
  upstream_model?: string
  effort: string
  status: BorrowTestResultStatus
  raw_answer: string
  raw_response: string
  raw_html: string
  html: string
  error: string
  started_at?: string
  finished_at?: string
  duration_ms?: number
  preview_url?: string
  preview_expires_at?: string
  preview_unavailable?: string
}

export interface BorrowTestTask {
  id: string
  client_task_id: string
  status: string
  created_at: string
  expires_at: string
  started_at?: string
  finished_at?: string
  total: number
  completed: number
  results?: BorrowTestResult[]
  error?: string
  replayed?: boolean
}

export interface BorrowTestRequest {
  client_task_id: string
  targets: Array<{ account_id: number; model_id: string; effort: string }>
}

export type BorrowTestEvent =
  | { type: 'task_start' | 'task_complete'; task: BorrowTestTask }
  | { type: 'result_started' | 'result_complete'; task_id: string; result: BorrowTestResult }
  | { type: 'error'; error?: string; message?: string }

export interface BorrowTestHistory {
  items: BorrowTestTask[]
  total: number
  page: number
  size: number
}

const basePath = '/admin/codex-gateway-borrow'

/** A new id belongs to a user submission, never to an HTTP attempt. */
export function createBorrowClientTaskId(serverOffsetMs = 0): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  // The server rejects every future timestamp. Leave one second for clock
  // precision and response transit while retaining the one-submit identity.
  let timestamp = Math.floor(Date.now() + serverOffsetMs - 1000)
  for (let index = 5; index >= 0; index--) {
    bytes[index] = timestamp % 256
    timestamp = Math.floor(timestamp / 256)
  }
  bytes[6] = (bytes[6] & 0x0f) | 0x70
  bytes[8] = (bytes[8] & 0x3f) | 0x80
  const hex = Array.from(bytes, value => value.toString(16).padStart(2, '0')).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}

/** No automatic retry: replaying this exact request is handled by its client_task_id. */
export async function streamBorrowTests(
  request: BorrowTestRequest,
  onEvent: (event: BorrowTestEvent) => void,
  signal: AbortSignal
): Promise<void> {
  const token = localStorage.getItem('auth_token')
  const response = await fetch(buildApiUrl(`${basePath}/tests`), {
    method: 'POST',
    headers: {
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      'Content-Type': 'application/json',
      Accept: 'text/event-stream',
      [ADMIN_UI_REQUEST_HEADER]: '1'
    },
    credentials: 'include',
    body: JSON.stringify(request),
    signal
  })
  if (!response.ok) {
    const body = await response.text()
    throw new Error(`HTTP ${response.status}${body ? `: ${body}` : ''}`)
  }
  if (!response.body) throw new Error('Borrow test stream has no response body')
  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  let complete = false
  const abort = () => { void reader.cancel().catch(() => {}) }
  signal.addEventListener('abort', abort, { once: true })

  function consume(block: string) {
    const data = block.split(/\r?\n/).filter(line => line.startsWith('data:'))
      .map(line => line.slice(5).replace(/^ /, '')).join('\n')
    if (!data) return
    const event = JSON.parse(data) as BorrowTestEvent
    if (event.type === 'error') throw new Error(event.error || event.message || data)
    onEvent(event)
    if (event.type === 'task_complete') complete = true
  }

  try {
    if (signal.aborted) throw new DOMException('Aborted', 'AbortError')
    while (!complete) {
      const { value, done } = await reader.read()
      if (signal.aborted) throw new DOMException('Aborted', 'AbortError')
      buffer += decoder.decode(value ?? new Uint8Array(), { stream: !done })
      let boundary: RegExpExecArray | null
      while ((boundary = /\r?\n\r?\n/.exec(buffer))) {
        const block = buffer.slice(0, boundary.index)
        buffer = buffer.slice(boundary.index + boundary[0].length)
        consume(block)
        if (complete) break
      }
      if (done) {
        if (buffer.trim() && !complete) consume(buffer)
        break
      }
    }
    if (!complete) throw new Error('Borrow test stream ended before task_complete')
  } finally {
    signal.removeEventListener('abort', abort)
    await reader.cancel().catch(() => {})
    reader.releaseLock()
  }
}
export async function streamBorrowDiagnostic(
  request: CodexBorrowDiagnosticRequest,
  onEvent: (event: CodexBorrowDiagnosticEvent) => void,
  signal: AbortSignal
): Promise<void> {
  const token = localStorage.getItem('auth_token')
  const response = await fetch(buildApiUrl(`${basePath}/diagnose`), {
    method: 'POST',
    headers: {
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      'Content-Type': 'application/json',
      Accept: 'text/event-stream',
      [ADMIN_UI_REQUEST_HEADER]: '1'
    },
    credentials: 'include',
    body: JSON.stringify(request),
    signal
  })
  if (!response.ok) {
    const body = await response.text()
    throw new Error(`HTTP ${response.status}${body ? `: ${body}` : ''}`)
  }
  if (!response.body) throw new Error('Borrow test stream has no response body')
  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  let complete = false
  const abort = () => { void reader.cancel().catch(() => {}) }
  signal.addEventListener('abort', abort, { once: true })

  function consume(block: string) {
    const data = block.split(/\r?\n/).filter(line => line.startsWith('data:'))
      .map(line => line.slice(5).replace(/^ /, '')).join('\n')
    if (!data) return
    const event = JSON.parse(data) as CodexBorrowDiagnosticEvent
    if (event.type === 'error') throw new Error(event.error || data)
    onEvent(event)
    if (event.type === 'done') complete = true
  }

  try {
    if (signal.aborted) throw new DOMException('Aborted', 'AbortError')
    while (!complete) {
      const { value, done } = await reader.read()
      if (signal.aborted) throw new DOMException('Aborted', 'AbortError')
      buffer += decoder.decode(value ?? new Uint8Array(), { stream: !done })
      let boundary: RegExpExecArray | null
      while ((boundary = /\r?\n\r?\n/.exec(buffer))) {
        const block = buffer.slice(0, boundary.index)
        buffer = buffer.slice(boundary.index + boundary[0].length)
        consume(block)
        if (complete) break
      }
      if (done) {
        if (buffer.trim() && !complete) consume(buffer)
        break
      }
    }
    if (!complete) throw new Error('Borrow test stream ended before done')
  } finally {
    signal.removeEventListener('abort', abort)
    await reader.cancel().catch(() => {})
    reader.releaseLock()
  }
}

export const codexGatewayBorrowAPI = {
  getConfig: async (signal?: AbortSignal) => (await apiClient.get<CodexGatewayBorrowConfig>(`${basePath}/config`, { signal })).data,
  saveConfig: async (config: CodexGatewayBorrowConfig, signal?: AbortSignal) => (await apiClient.put<CodexGatewayBorrowConfig>(`${basePath}/config`, config, { signal })).data,
  getStatus: async (signal?: AbortSignal) => (await apiClient.get<CodexGatewayBorrowStatus>(`${basePath}/status`, { signal })).data,
  prepare: async (signal?: AbortSignal) => (await apiClient.post<CodexGatewayBorrowStatus>(`${basePath}/prepare`, {}, { signal, timeout: 0 })).data,
  verify: async (accountId: number, model: string, signal?: AbortSignal) => (await apiClient.post<BorrowVerification>(`${basePath}/verify`, { account_id: accountId, model }, { signal, timeout: 0 })).data,
  listTests: async (page = 1, signal?: AbortSignal) => (await apiClient.get<BorrowTestHistory>(`${basePath}/tests`, { params: { page, size: 12 }, signal })).data,
  getTest: async (id: string, signal?: AbortSignal) => (await apiClient.get<BorrowTestTask>(`${basePath}/tests/${encodeURIComponent(id)}`, { signal })).data,
  streamTests: streamBorrowTests,
  diagnose: streamBorrowDiagnostic
}

export default codexGatewayBorrowAPI
