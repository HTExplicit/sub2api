import { flushPromises, shallowMount, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import OpsErrorDetailModal from '../OpsErrorDetailModal.vue'

const mocks = vi.hoisted(() => ({
  getRequestErrorDetail: vi.fn(),
  listRequestErrorUpstreamErrors: vi.fn()
}))

vi.mock('@/api/admin/ops', () => ({
  opsAPI: {
    getRequestErrorDetail: mocks.getRequestErrorDetail,
    getUpstreamErrorDetail: vi.fn(),
    listRequestErrorUpstreamErrors: mocks.listRequestErrorUpstreamErrors
  }
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({ showError: vi.fn() })
}))

vi.mock('@/components/admin/codex/CodexContinuationDiagnostics.vue', () => ({
  default: { name: 'CodexContinuationDiagnostics', props: ['errorId'], template: '<div />' }
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

async function mountErrorDetail(upstreamErrors: string, upstreamStatusCode = 403) {
  mocks.getRequestErrorDetail.mockResolvedValue({
    id: 1,
    created_at: '2026-10-08T08:00:00Z',
    phase: 'request',
    type: 'upstream_error',
    error_owner: 'provider',
    status_code: upstreamStatusCode,
    upstream_status_code: upstreamStatusCode,
    upstream_errors: upstreamErrors,
    upstream_error_message: 'Original upstream error',
    platform: 'anthropic'
  })
  const wrapper = shallowMount(OpsErrorDetailModal, {
    props: { show: true, errorId: 1, errorType: 'request' },
    global: {
      stubs: {
        BaseDialog: { template: '<div><slot /></div>' },
        Icon: true
      }
    }
  })
  await flushPromises()
  return wrapper
}

function summaryCard(wrapper: VueWrapper, label: string) {
  return wrapper.findAll('[data-ui="ops-kv-grid"] > div').find(card => card.find('div').text() === label)
}

describe('OpsErrorDetailModal', () => {
  beforeEach(() => {
    mocks.getRequestErrorDetail.mockReset()
    mocks.listRequestErrorUpstreamErrors.mockReset()
    mocks.listRequestErrorUpstreamErrors.mockResolvedValue({ items: [] })
  })

  it('prioritizes upstream root cause and deduplicates diagnostic payloads', async () => {
    mocks.getRequestErrorDetail.mockResolvedValue({
      id: 1,
      created_at: '2026-08-19T00:00:00Z',
      phase: 'request',
      type: 'upstream_error',
      error_owner: 'provider',
      error_source: 'gateway',
      severity: 'P1',
      status_code: 502,
      upstream_status_code: 429,
      platform: 'openai',
      model: 'gpt-5.6',
      resolved: false,
      request_id: 'rid-1',
      message: 'All available accounts exhausted',
      error_body: '{"error":"same"}',
      upstream_error_message: 'provider rate limit exhausted',
      upstream_error_detail: '{"error":"same"}',
      upstream_errors: '[]',
      account_name: 'account',
      group_name: 'group',
      is_business_limited: false
    })

    const wrapper = shallowMount(OpsErrorDetailModal, {
      props: { show: true, errorId: 1, errorType: 'request' },
      global: {
        stubs: {
          BaseDialog: { template: '<div><slot /></div>' },
          Icon: true
        }
      }
    })
    await flushPromises()

    expect(wrapper.text()).toContain('provider rate limit exhausted')
    expect(wrapper.text()).toContain('admin.ops.errorDetail.upstreamStatus')
    expect(wrapper.text()).toContain('429')
    expect(wrapper.findAll('pre')).toHaveLength(2)
    expect(wrapper.text()).not.toContain('admin.ops.errorDetail.payloads.upstream_detail')
    expect(wrapper.findComponent({ name: 'CodexContinuationDiagnostics' }).props('errorId')).toBe(1)
  })

  it.each([400, 529])('separates stream error %i from the actual upstream HTTP response', async (semanticStatus) => {
    const events = [{
      kind: 'stream_error',
      upstream_status_code: semanticStatus,
      upstream_http_status_code: 200,
      message: 'Original stream error',
      detail: '{"signature_comparison":{"omitted":0}}'
    }]
    const wrapper = await mountErrorDetail(JSON.stringify(events))

    expect(summaryCard(wrapper, 'admin.ops.errorDetail.upstreamStreamStatus')?.text()).toContain(String(semanticStatus))
    expect(summaryCard(wrapper, 'admin.ops.errorDetail.upstreamHttpStatus')?.text()).toContain('200')
    expect(summaryCard(wrapper, 'admin.ops.errorDetail.upstreamStatus')).toBeUndefined()
    expect(wrapper.text()).toContain('Original upstream error')
    expect(wrapper.findAll('pre').some(pre => pre.text() === JSON.stringify(events, null, 2))).toBe(true)
  })

  it('keeps the final HTTP error visible after an earlier stream error', async () => {
    const wrapper = await mountErrorDetail(JSON.stringify([
      { kind: 'stream_error', upstream_status_code: 400, upstream_http_status_code: 200 },
      { kind: 'http_error', upstream_status_code: 403 }
    ]))

    expect(summaryCard(wrapper, 'admin.ops.errorDetail.upstreamStatus')?.text()).toContain('403')
    expect(summaryCard(wrapper, 'admin.ops.errorDetail.upstreamStreamStatus')).toBeUndefined()
    expect(summaryCard(wrapper, 'admin.ops.errorDetail.upstreamHttpStatus')).toBeUndefined()
  })

  it.each([
    ['historical stream error', '[{"kind":"stream_error","upstream_status_code":403}]'],
    ['ordinary HTTP error', '[{"kind":"http_error","upstream_status_code":403,"upstream_http_status_code":403}]'],
    ['invalid JSON', '{invalid'],
    ['JSON object', '{"kind":"stream_error","upstream_status_code":400,"upstream_http_status_code":200}'],
    ['null final event', '[null]'],
    ['zero HTTP code', '[{"kind":"stream_error","upstream_status_code":400,"upstream_http_status_code":0}]'],
    ['negative HTTP code', '[{"kind":"stream_error","upstream_status_code":400,"upstream_http_status_code":-1}]'],
    ['string HTTP code', '[{"kind":"stream_error","upstream_status_code":400,"upstream_http_status_code":"200"}]'],
    ['out-of-range HTTP code', '[{"kind":"stream_error","upstream_status_code":400,"upstream_http_status_code":999}]'],
    ['success semantic code', '[{"kind":"stream_error","upstream_status_code":200,"upstream_http_status_code":200}]'],
    ['out-of-range semantic code', '[{"kind":"stream_error","upstream_status_code":999,"upstream_http_status_code":200}]'],
    ['missing semantic code', '[{"kind":"stream_error","upstream_http_status_code":200}]']
  ])('preserves the existing status display for %s', async (_name, rawEvents) => {
    const wrapper = await mountErrorDetail(rawEvents)

    expect(summaryCard(wrapper, 'admin.ops.errorDetail.upstreamStatus')?.text()).toContain('403')
    expect(summaryCard(wrapper, 'admin.ops.errorDetail.upstreamStreamStatus')).toBeUndefined()
    expect(summaryCard(wrapper, 'admin.ops.errorDetail.upstreamHttpStatus')).toBeUndefined()
    expect(wrapper.text()).toContain('Original upstream error')
  })
})
