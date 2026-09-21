import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import CindyBalanceProbePanel from '../CindyBalanceProbePanel.vue'
import type { ResourceInput } from '@sub2api/plugin-ui/client'
import type { CindyBalanceProbeJob, CindyBalanceProbePreviewRequest } from '../api'

enableAutoUnmount(afterEach)

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const preferences = new Map<string, string>()
const preferenceListeners = new Set<(key: string, value: string) => void>()
const calls: Array<{ operation: string; input: ResourceInput }> = []
const notifications: Array<{ type: string; fields: Record<string, unknown> }> = []
const job = (id: number, rate = 0.5): CindyBalanceProbeJob => ({
  id, status: 'running', scope: { mode: 'all' }, rate_rps: rate,
  candidate_count: 2, candidate_fingerprint: 'second-review-job', request_count: 0,
  consecutive_upstream_failures: 0, created_at: '2026-09-21T00:00:00Z', updated_at: '2026-09-21T00:00:00Z',
  counts: { pending: 2, running: 0, healthy: 0, recovered: 0, exhausted: 0, inconclusive: 0, skipped: 0 },
})
let listedJobs: CindyBalanceProbeJob[] = []
const missingJobIDs = new Set<number>()

// Keep the production component, named API and public SDK. Only the iframe
// transport is replaced; every response is a fresh HTTP-like JSON clone.
class ReviewBridge {
  async preference(key: string) { return preferences.get(key) ?? null }
  async savePreference(key: string, value: string) {
    preferences.set(key, value)
    for (const listener of preferenceListeners) listener(key, value)
  }
  onPreferenceChange(listener: (key: string, value: string) => void) {
    preferenceListeners.add(listener)
    return () => preferenceListeners.delete(listener)
  }
  notify(type: string, fields: Record<string, unknown>) { notifications.push({ type, fields }) }
  async resource(operation: string, input: ResourceInput) {
    calls.push({ operation, input: JSON.parse(JSON.stringify(input)) as ResourceInput })
    if (operation === 'cindy.probe.list') return JSON.parse(JSON.stringify({ items: listedJobs, total: listedJobs.length }))
    if (operation === 'cindy.probe.get') {
      const id = Number(input.params?.id)
      if (missingJobIDs.has(id)) throw new Error('Historical job no longer exists')
      return JSON.parse(JSON.stringify(job(id, 0.6)))
    }
    if (operation === 'cindy.probe.items') return { items: [], total: 0, page: 1, page_size: 20 }
    if (operation === 'cindy.probe.preview') {
      const body = input.body as CindyBalanceProbePreviewRequest
      return JSON.parse(JSON.stringify({ ...body, candidate_count: 1, candidate_fingerprint: 'second-review-preview',
        marked_count: 0, unmarked_count: 1, minimum_calls: 1, maximum_calls: 2,
        minimum_eta_seconds: 1, maximum_eta_seconds: 2 }))
    }
    throw new Error(`Unexpected mutation in draft review: ${operation}`)
  }
}

function render(selectedIds: number[] = [9, 10]) {
  return mount(CindyBalanceProbePanel, {
    props: { selectedIds, filters: { statuses: ['active'] }, initiallyExpanded: true },
    global: { stubs: { Icon: true, ConfirmDialog: true } },
  })
}

describe('Cindy UI second review', () => {
  beforeEach(() => {
    preferences.clear()
    preferenceListeners.clear()
    calls.length = 0
    notifications.length = 0
    listedJobs = [job(7), job(8, 0.6)]
    missingJobIDs.clear()
    vi.stubGlobal('Sub2APIPluginBridge', ReviewBridge)
  })

  afterEach(() => { vi.unstubAllGlobals() })

  it('retains probe scope and rate drafts on remount but re-previews only current host accounts', async () => {
    const original = render()
    await flushPromises()
    await original.get('[data-test="cindy-probe-scope-selected"]').trigger('click')
    await original.get('[data-test="cindy-probe-rate"]').setValue('0.8')
    await original.get('[data-test="cindy-probe-job-select"]').setValue('8')
    await original.get('[data-test="cindy-probe-job-rate"]').setValue('0.9')
    original.unmount()

    const reopened = render([11])
    await flushPromises()
    expect.soft(reopened.get<HTMLInputElement>('[data-test="cindy-probe-rate"]').element.value).toBe('0.8')
    expect.soft(reopened.get<HTMLSelectElement>('[data-test="cindy-probe-job-select"]').element.value).toBe('8')
    expect.soft(reopened.get<HTMLInputElement>('[data-test="cindy-probe-job-rate"]').element.value).toBe('0.9')
    expect(reopened.find('[data-test="cindy-probe-preview-result"]').exists()).toBe(false)
    expect(calls.every(call => ['cindy.probe.list', 'cindy.probe.get', 'cindy.probe.items'].includes(call.operation))).toBe(true)

    await reopened.get('[data-test="cindy-probe-preview"]').trigger('click')
    await flushPromises()
    const requested = calls.filter(call => call.operation === 'cindy.probe.preview')
    expect(requested).toHaveLength(1)
    expect.soft(requested[0]?.input.body).toEqual({ scope: { mode: 'selected', account_ids: [11] }, rate_rps: 0.8 })
    expect(calls.some(call => /\.(create|resume|pause|cancel|rate)$/.test(call.operation))).toBe(false)
    const saved = JSON.parse(preferences.get('cindy-balance-probe') || '{}')
    expect(Object.keys(saved).sort()).toEqual(['job_rate_rps', 'rate_rps', 'scope_mode', 'selected_job_id', 'version'])
    console.info(JSON.stringify({ case: 'probe-remount', actual_preview_request: requested[0]?.input.body,
      stored_keys: [...preferences.keys()], upstream: 'fake transport only' }))
  })

  it('rejects malformed scope drafts and ignores late preference updates after local edits', async () => {
    preferences.set('cindy-balance-probe', JSON.stringify({ version: 1, scope_mode: ['selected'], rate_rps: 0.8,
      selected_job_id: 8, job_rate_rps: 0.9 }))
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get<HTMLInputElement>('[data-test="cindy-probe-rate"]').element.value).toBe('0.5')
    expect(wrapper.get<HTMLSelectElement>('[data-test="cindy-probe-job-select"]').element.value).toBe('7')
    expect(calls.some(call => call.operation === 'cindy.probe.preview')).toBe(false)

    await wrapper.get('[data-test="cindy-probe-scope-filter"]').trigger('click')
    await wrapper.get('[data-test="cindy-probe-rate"]').setValue('0.7')
    const stale = JSON.stringify({ version: 1, scope_mode: 'selected', rate_rps: 0.9,
      selected_job_id: 8, job_rate_rps: 0.8 })
    for (const listener of preferenceListeners) listener('cindy-balance-probe', stale)
    await flushPromises()
    expect(wrapper.get<HTMLInputElement>('[data-test="cindy-probe-rate"]').element.value).toBe('0.7')
    expect(wrapper.get<HTMLSelectElement>('[data-test="cindy-probe-job-select"]').element.value).toBe('7')
    await wrapper.get('[data-test="cindy-probe-preview"]').trigger('click')
    await flushPromises()
    expect(calls.find(call => call.operation === 'cindy.probe.preview')?.input.body).toEqual({
      scope: { mode: 'filter', filters: { statuses: ['active'] } }, rate_rps: 0.7,
    })
    expect(calls.some(call => /\.(create|resume|pause|cancel|rate)$/.test(call.operation))).toBe(false)
  })

  it('restores an older job through retained reads and never broadens an empty selected scope on lookup failure', async () => {
    listedJobs = Array.from({ length: 10 }, (_, index) => job(100 + index))
    preferences.set('cindy-balance-probe', JSON.stringify({ version: 1, scope_mode: 'selected', rate_rps: 0.8,
      selected_job_id: 42, job_rate_rps: 0.9 }))
    const original = render()
    await flushPromises()
    expect(original.get<HTMLSelectElement>('[data-test="cindy-probe-job-select"]').element.value).toBe('42')
    expect(original.get<HTMLInputElement>('[data-test="cindy-probe-job-rate"]').element.value).toBe('0.9')
    expect(calls.filter(call => call.operation === 'cindy.probe.get').map(call => call.input.params?.id)).toEqual([42])
    expect(original.get('[data-test="cindy-probe-job-select"]').findAll('option')).toHaveLength(12)
    original.unmount()

    calls.length = 0
    missingJobIDs.add(42)
    const missing = render([])
    await flushPromises()
    expect(missing.get<HTMLSelectElement>('[data-test="cindy-probe-job-select"]').element.value).toBe('42')
    expect(missing.find('[data-test="cindy-probe-job-detail"]').exists()).toBe(false)
    expect(missing.get('[data-test="cindy-probe-preview"]').attributes('disabled')).toBeDefined()
    expect(calls.filter(call => call.operation === 'cindy.probe.get').map(call => call.input.params?.id)).toEqual([42])
    expect(calls.every(call => ['cindy.probe.list', 'cindy.probe.get', 'cindy.probe.items'].includes(call.operation))).toBe(true)
    expect(notifications.some(item => item.fields.level === 'error')).toBe(true)
    console.info(JSON.stringify({ case: 'missing-history', selected_job: 42, host_selected_accounts: [],
      preview_disabled: true, mutations: 0 }))
  })
})
