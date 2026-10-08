import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { pelicanTestsAPI, streamPelicanTests, type PelicanTestRequest } from '../pelicanTests'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: mocks, buildApiUrl: (path: string) => `/api/v1${path}` }))

const request: PelicanTestRequest = {
  client_task_id: '019ae333-ffff-7000-8000-000000000001', generation_timeout_seconds: 600,
  targets: [{ account_id: 2, model_id: 'gpt-6.1-sol', effort: 'high' }]
}
function response(chunks: string[]) {
  const encoder = new TextEncoder()
  return new Response(new ReadableStream({
    start(controller) { for (const chunk of chunks) controller.enqueue(encoder.encode(chunk)); controller.close() }
  }), { headers: { 'Content-Type': 'text/event-stream' } })
}

describe('Standalone Pelican transport', () => {
  beforeEach(() => { vi.clearAllMocks(); localStorage.setItem('auth_token', 'admin-test-token') })
  afterEach(() => { vi.unstubAllGlobals(); localStorage.clear() })

  it('uses only independent local metadata and stored test endpoints for page reads', async () => {
    mocks.get.mockResolvedValue({ data: { accounts: [] } })
    mocks.post.mockResolvedValue({ data: { accounts: [] } })
    const signal = new AbortController().signal
    await pelicanTestsAPI.getOptions(signal)
    await pelicanTestsAPI.getAccountOptions([1, 2], signal)
    await pelicanTestsAPI.listTests(2, signal)
    await pelicanTestsAPI.getTest('saved-id', signal)
    expect(mocks.get.mock.calls).toEqual([
      ['/admin/pelican-tests/options', { signal }],
      ['/admin/pelican-tests/tests', { params: { page: 2, size: 12 }, signal }],
      ['/admin/pelican-tests/tests/saved-id', { signal }]
    ])
    expect(mocks.post).toHaveBeenCalledOnce()
    expect(mocks.post).toHaveBeenCalledWith('/admin/pelican-tests/options', { account_ids: [1, 2] }, { signal })
  })

  it('posts one budgeted submission, parses phase events across CRLF chunks, and ends at the stored task terminal event', async () => {
    const fetch = vi.fn().mockResolvedValue(response([
      ': keep-alive\r\n\r\ndata: {"type":"task_start","task":{"id":"server-id"}}\r\n\r\n',
      'data: {"type":"result_phase","task_id":"server-id","phase":"pre',
      'paring","result":{"id":"one","status":"running"}}\r\n\r\ndata: {"type":"task_complete",\r\ndata: "task":{"id":"server-id","status":"complete"}}\r\n\r\n'
    ]))
    vi.stubGlobal('fetch', fetch)
    const event = vi.fn()
    const signal = new AbortController().signal
    await streamPelicanTests(request, event, signal)
    expect(fetch).toHaveBeenCalledOnce()
    expect(fetch).toHaveBeenCalledWith('/api/v1/admin/pelican-tests/tests', expect.objectContaining({
      method: 'POST', body: JSON.stringify(request), signal,
      headers: expect.objectContaining({ Authorization: 'Bearer admin-test-token', 'X-Admin-UI-Request': '1' })
    }))
    expect(event.mock.calls.map(([value]) => value.type)).toEqual(['task_start', 'result_phase', 'task_complete'])
    expect(event.mock.calls[1][0].phase).toBe('preparing')
  })

  it('preserves full HTTP and in-stream errors and rejects unfinished streams without retrying', async () => {
    const body = 'upstream full error\n' + 'x'.repeat(14000)
    const fetch = vi.fn().mockResolvedValueOnce(new Response(body, { status: 429 }))
      .mockResolvedValueOnce(response([`data: ${JSON.stringify({ type: 'error', error: body })}\n\n`]))
      .mockResolvedValueOnce(response(['data: {"type":"task_start","task":{"id":"one"}}\n\n']))
    vi.stubGlobal('fetch', fetch)
    await expect(streamPelicanTests(request, vi.fn(), new AbortController().signal)).rejects.toThrow(body)
    await expect(streamPelicanTests(request, vi.fn(), new AbortController().signal)).rejects.toThrow(body)
    await expect(streamPelicanTests(request, vi.fn(), new AbortController().signal)).rejects.toThrow('ended before task_complete')
    expect(fetch).toHaveBeenCalledTimes(3)
  })

  it('cancels the pending stream when stop or page leave aborts the request', async () => {
    const cancel = vi.fn()
    const fetch = vi.fn().mockResolvedValue(new Response(new ReadableStream({ cancel })))
    vi.stubGlobal('fetch', fetch)
    const controller = new AbortController()
    const pending = streamPelicanTests(request, vi.fn(), controller.signal)
    await Promise.resolve()
    await Promise.resolve()
    controller.abort()
    await expect(pending).rejects.toMatchObject({ name: 'AbortError' })
    expect(cancel).toHaveBeenCalledTimes(1)
    expect(fetch).toHaveBeenCalledTimes(1)
  })
})
