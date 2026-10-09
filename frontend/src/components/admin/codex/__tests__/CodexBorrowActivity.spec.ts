import { describe, expect, it, vi, beforeEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import CodexBorrowActivity from '../CodexBorrowActivity.vue'
import type { CodexBorrowDiagnosticEvent, CodexGatewayBorrowStatus } from '@/api/admin/codexGatewayBorrow'

const { diagnose } = vi.hoisted(() => ({ diagnose: vi.fn() }))
vi.mock('@/api/admin/codexGatewayBorrow', () => ({ codexGatewayBorrowAPI: { diagnose } }))
const status: CodexGatewayBorrowStatus = {
  enabled: true, revision: 1, generated_at: new Date().toISOString(), preparing: false,
  config: { enabled: true, source_account_ids: [1], target_account_ids: [2], models: ['gpt-6-astra'] }, sources: [], targets: [], recent_usage: []
}
function open(locale = 'zh') {
  return mount(CodexBorrowActivity, { props: { status, accountName: id => `Account ${id}` }, global: {
    plugins: [createI18n({ legacy: false, locale, messages: { zh: {}, en: {} } })],
    stubs: { RouterLink: { template: '<a><slot /></a>' } }
  } })
}
beforeEach(() => { diagnose.mockReset() })

describe('Codex actual-request diagnostics', () => {
  it('reads existing evidence without automatically issuing requests', () => {
    const wrapper = open()
    expect(diagnose).not.toHaveBeenCalled()
    expect(wrapper.get('[data-test="borrow-no-usage"]').text()).toContain('还没有实际发送记录')
    expect(wrapper.get('[data-test="diagnose-selection"]').element.tagName).toBe('SELECT')
    wrapper.unmount()
  })

  it('requires an explicit action and shows applied separately from completed', async () => {
    diagnose.mockImplementation(async (_request, emit: (event: CodexBorrowDiagnosticEvent) => void) => {
      emit({ type: 'result', requests: 3, limit: 8, result: { mode: 'borrowed', turn: 1, applied: true, completed: false, reported_model: 'gpt-6-astra', answer: '', raw_response: '{"error":"raw upstream failure"}', error: 'raw upstream failure', duration_ms: 10 } })
      emit({ type: 'done', requests: 3, limit: 8 })
    })
    const wrapper = open('en')
    await wrapper.get('[data-test="diagnose-start"]').trigger('click')
    await flushPromises()
    expect(diagnose).toHaveBeenCalledOnce()
    expect(diagnose.mock.calls[0][0]).toEqual({ account_id: 2, model: 'gpt-6-astra', transport: 'http' })
    const result = wrapper.get('[data-test="diagnose-result"]')
    expect(result.text()).toContain('not completed')
    expect(result.text()).toContain('Borrow was applied')
    expect(result.text()).toContain('raw upstream failure')
    expect(wrapper.emitted('refresh')).toHaveLength(1)
    wrapper.unmount()
  })

  it('cancels its explicit diagnostic when the page is closed', async () => {
    let signal: AbortSignal | undefined
    diagnose.mockImplementation((_request, _emit, abort: AbortSignal) => { signal = abort; return new Promise((_resolve, reject) => abort.addEventListener('abort', () => reject(new Error('aborted')), { once: true })) })
    const wrapper = open()
    await wrapper.get('[data-test="diagnose-start"]').trigger('click')
    wrapper.unmount()
    await flushPromises()
    expect(signal?.aborted).toBe(true)
    expect(wrapper.emitted('refresh')).toBeUndefined()
  })
})
