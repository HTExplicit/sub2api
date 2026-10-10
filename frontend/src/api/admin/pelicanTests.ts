import { apiClient, buildApiUrl } from '../client'
import { ADMIN_UI_REQUEST_HEADER } from '../adminUIRequest'
import type { BorrowTestResult, BorrowTestTask } from './codexGatewayBorrow'

export { createBorrowClientTaskId as createPelicanClientTaskId, PELICAN_BORROW_PROMPT as PELICAN_PROMPT } from './codexGatewayBorrow'

export interface PelicanModelOption {
  id: string
  display_name: string
  upstream_model: string
  reasoning_efforts: string[]
  default_effort: string
  text_supported: boolean
  capability_reason: string
}

export interface PelicanAccountOption {
  id: number
  name: string
  platform: string
  type: string
  status: string
  schedulable: boolean
  parent_account_id: number | null
  proxy_id: number | null
  proxy_name: string
  rate_limited_until: string | null
  overload_until: string | null
  temp_unschedulable_until: string | null
  error_message?: string
  capability_reason: string
  manual_model_allowed: boolean
  default_model_id: string
  models: PelicanModelOption[]
}

export interface PelicanOptions {
  generated_at: string
  max_concurrency: number
  default_generation_timeout_seconds: number
  min_generation_timeout_seconds: number
  max_generation_timeout_seconds: number
  accounts: PelicanAccountOption[]
}

export type PelicanPhase = 'queued' | 'preparing' | 'generating'

export interface PelicanTestResult extends BorrowTestResult {
  platform?: string
  actual_endpoint?: string
  actual_protocol?: string
  actual_transport?: string
  borrow_applied?: boolean
  queue_duration_ms?: number
  preparation_duration_ms?: number
  generation_duration_ms?: number
  generation_started_at?: string
  phase?: PelicanPhase
}

export interface PelicanTestTask extends Omit<BorrowTestTask, 'results'> {
  generation_timeout_seconds?: number
  execution_mode?: string
  results?: PelicanTestResult[]
}

export interface PelicanTestRequest {
  client_task_id: string
  generation_timeout_seconds: number
  targets: Array<{ account_id: number; model_id: string; effort: string }>
}

export type PelicanTestEvent =
  | { type: 'task_start' | 'task_complete'; task: PelicanTestTask }
  | { type: 'result_started' | 'result_complete'; task_id: string; result: PelicanTestResult }
  | { type: 'result_phase'; task_id: string; result: PelicanTestResult; phase: PelicanPhase }
  | { type: 'error'; error?: string; message?: string }

export interface PelicanTestHistory {
  items: PelicanTestTask[]
  total: number
  page: number
  size: number
}

const basePath = '/admin/pelican-tests'

/** One user submission has one identity and one request; an interrupted stream is not retried. */
export async function streamPelicanTests(
  request: PelicanTestRequest,
  onEvent: (event: PelicanTestEvent) => void,
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
  if (!response.body) throw new Error('Pelican test stream has no response body')
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
    const event = JSON.parse(data) as PelicanTestEvent
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
    if (!complete) throw new Error('Pelican test stream ended before task_complete')
  } finally {
    signal.removeEventListener('abort', abort)
    await reader.cancel().catch(() => {})
    reader.releaseLock()
  }
}

export interface PelicanResultSummary {
  id: string; task_id: string; ordinal: number; account_id: number; account_name: string
  platform: string; model_id: string; effort: string; status: string; phase?: PelicanPhase
  has_preview: boolean; interrupted: boolean; started_at?: string; finished_at?: string
  generation_started_at?: string; duration_ms: number; queue_duration_ms: number
  preparation_duration_ms: number; generation_duration_ms: number; expires_at: string
}
export interface PelicanTaskSnapshot extends PelicanTestTask { prompt: string; counts: Record<string, number> }
export interface PelicanResultPage { items: PelicanResultSummary[]; total: number; page: number; page_size: number }
export interface PelicanResultFilter { page?: number; page_size?: number; account_id?: number; model?: string; status?: string }
export interface PelicanTaskEvent { type: 'snapshot' | 'task_complete'; task: PelicanTaskSnapshot }

export async function observePelicanTask(id: string, onEvent: (event: PelicanTaskEvent) => void, signal: AbortSignal): Promise<void> {
  const token = localStorage.getItem('auth_token')
  const response = await fetch(buildApiUrl(`${basePath}/tasks/${encodeURIComponent(id)}/events`), {
    method: 'GET', credentials: 'include', signal,
    headers: { ...(token ? { Authorization: `Bearer ${token}` } : {}), Accept: 'text/event-stream', [ADMIN_UI_REQUEST_HEADER]: '1' }
  })
  if (!response.ok) throw new Error(`HTTP ${response.status}: ${await response.text()}`)
  if (!response.body) throw new Error('Task observation has no response body')
  const reader = response.body.getReader(), decoder = new TextDecoder()
  let buffer = '', complete = false
  const abort = () => { void reader.cancel().catch(() => {}) }
  signal.addEventListener('abort', abort, { once: true })
  try {
    while (!complete && !signal.aborted) {
      const { value, done } = await reader.read()
      if (signal.aborted) return
      buffer += decoder.decode(value ?? new Uint8Array(), { stream: !done })
      let boundary: RegExpExecArray | null
      while ((boundary = /\r?\n\r?\n/.exec(buffer))) {
        const block = buffer.slice(0, boundary.index)
        buffer = buffer.slice(boundary.index + boundary[0].length)
        const data = block.split(/\r?\n/).filter(line => line.startsWith('data:')).map(line => line.slice(5).trimStart()).join('\n')
        if (!data) continue
        const event = JSON.parse(data) as PelicanTaskEvent
        if (event.type !== 'snapshot' && event.type !== 'task_complete') continue
        onEvent(event)
        if (event.type === 'task_complete') { complete = true; break }
      }
      if (done) break
    }
    if (!complete && !signal.aborted) throw new Error('Task observation disconnected')
  } finally {
    signal.removeEventListener('abort', abort)
    await reader.cancel().catch(() => {})
    reader.releaseLock()
  }
}

export const pelicanTestsAPI = {
  startTask: async (request: PelicanTestRequest, signal?: AbortSignal) => (await apiClient.post<PelicanTaskSnapshot>(`${basePath}/tasks`, request, { signal })).data,
  listTasks: async (page = 1, signal?: AbortSignal) => (await apiClient.get<PelicanTestHistory>(`${basePath}/tasks`, { params: { page, page_size: 12 }, signal })).data,
  getTask: async (id: string, signal?: AbortSignal) => (await apiClient.get<PelicanTaskSnapshot>(`${basePath}/tasks/${encodeURIComponent(id)}`, { signal })).data,
  getResults: async (id: string, filters: PelicanResultFilter = {}, signal?: AbortSignal) => (await apiClient.get<PelicanResultPage>(`${basePath}/tasks/${encodeURIComponent(id)}/results`, { params: { page: 1, page_size: 24, ...filters }, signal })).data,
  getResult: async (task: string, id: string, signal?: AbortSignal) => (await apiClient.get<PelicanTestResult>(`${basePath}/tasks/${encodeURIComponent(task)}/results/${encodeURIComponent(id)}`, { signal })).data,
  cancelTask: async (id: string) => (await apiClient.post<PelicanTaskSnapshot>(`${basePath}/tasks/${encodeURIComponent(id)}/cancel`)).data,
  observeTask: observePelicanTask,
  getOptions: async (signal?: AbortSignal) => (await apiClient.get<PelicanOptions>(`${basePath}/options`, { signal })).data,
  getAccountOptions: async (accountIds: number[], signal?: AbortSignal) => (await apiClient.post<PelicanOptions>(`${basePath}/options`, { account_ids: accountIds }, { signal })).data,
  listTests: async (page = 1, signal?: AbortSignal) => (await apiClient.get<PelicanTestHistory>(`${basePath}/tests`, { params: { page, size: 12 }, signal })).data,
  getTest: async (id: string, signal?: AbortSignal) => (await apiClient.get<PelicanTestTask>(`${basePath}/tests/${encodeURIComponent(id)}`, { signal })).data,
  streamTests: streamPelicanTests
}

export default pelicanTestsAPI
