import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createBorrowClientTaskId, streamBorrowTests, type BorrowTestRequest } from '../codexGatewayBorrow'

vi.mock('@/api/client', () => ({ apiClient: {}, buildApiUrl: (path: string) => `/api/v1${path}` }))

const request: BorrowTestRequest = {
  client_task_id: '019ae333-ffff-7000-8000-000000000001',
  targets: [{ account_id: 2, model_id: 'gpt-6.1-sol', effort: 'high' }]
}

function response(chunks: string[]) {
  const encoder = new TextEncoder()
  return new Response(new ReadableStream({
    start(controller) { for (const chunk of chunks) controller.enqueue(encoder.encode(chunk)); controller.close() }
  }), { headers: { 'Content-Type': 'text/event-stream' } })
}

describe('Codex gateway borrow test transport', () => {
  beforeEach(() => { localStorage.setItem('auth_token', 'admin-test-token') })
  afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers(); localStorage.clear() })

  it('mints UUIDv7 using the server clock offset without storing task or history data', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-10-08T00:00:00Z'))
    const id = createBorrowClientTaskId(90_000)
    const otherId = createBorrowClientTaskId(90_000)
    expect(id).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/)
    expect(parseInt(id.replace(/-/g, '').slice(0, 12), 16)).toBe(Date.now() + 90_000 - 1000)
    expect(otherId).not.toBe(id)
    expect(localStorage.length).toBe(1)
  })

  it('uses the same submission body once, parses split CRLF SSE, and stops at task_complete', async () => {
    const fetch = vi.fn().mockResolvedValue(response([
      ': keep-alive\r\n\r\ndata: {"type":"task_start","task":{"id":"server-id"}}\r\n\r\n',
      'data: {"type":"result_complete","task_id":"server-id","result":{"id":"one","status":"sk',
      'ipped"}}\r\n\r\ndata: {"type":"task_complete",\r\ndata: "task":{"id":"server-id","status":"complete"}}\r\n\r\n'
    ]))
    vi.stubGlobal('fetch', fetch)
    const event = vi.fn()
    const controller = new AbortController()
    await streamBorrowTests(request, event, controller.signal)
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(fetch).toHaveBeenCalledWith('/api/v1/admin/codex-gateway-borrow/tests', expect.objectContaining({
      method: 'POST', body: JSON.stringify(request), signal: controller.signal,
      headers: expect.objectContaining({ Authorization: 'Bearer admin-test-token', 'X-Admin-UI-Request': '1' })
    }))
    expect(event.mock.calls.map(([value]) => value.type)).toEqual(['task_start', 'result_complete', 'task_complete'])
    expect(event.mock.calls[1][0].result.status).toBe('skipped')
  })

  it('reports incomplete streams and full HTTP failure bodies without retrying', async () => {
    const body = 'upstream full error\n' + 'x'.repeat(14000)
    const fetch = vi.fn()
      .mockResolvedValueOnce(new Response(body, { status: 429 }))
      .mockResolvedValueOnce(response(['data: {"type":"task_start","task":{"id":"one"}}\n\n']))
    vi.stubGlobal('fetch', fetch)
    await expect(streamBorrowTests(request, vi.fn(), new AbortController().signal)).rejects.toThrow(body)
    await expect(streamBorrowTests(request, vi.fn(), new AbortController().signal)).rejects.toThrow('ended before task_complete')
    expect(fetch).toHaveBeenCalledTimes(2)
  })

  it('cancels a pending reader when the caller aborts', async () => {
    const cancel = vi.fn()
    const fetch = vi.fn().mockResolvedValue(new Response(new ReadableStream({ cancel })))
    vi.stubGlobal('fetch', fetch)
    const controller = new AbortController()
    const pending = streamBorrowTests(request, vi.fn(), controller.signal)
    await Promise.resolve()
    await Promise.resolve()
    controller.abort()
    await expect(pending).rejects.toMatchObject({ name: 'AbortError' })
    expect(cancel).toHaveBeenCalledTimes(1)
    expect(fetch).toHaveBeenCalledTimes(1)
  })
})
