import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import type { PromptAuditConfig, PromptAuditDraft, PromptAuditEvent, PromptAuditRuntime, PromptDeletePreview } from '../types'
import { emptyEventFilters, SCANNER_CATALOG } from '../viewModel'
import PromptAuditView from '../PromptAuditView.vue'

const mocks = vi.hoisted(() => ({
  getConfig: vi.fn(), updateConfig: vi.fn(), probeEndpoint: vi.fn(), getRuntime: vi.fn(), listEvents: vi.fn(),
  getEvent: vi.fn(), deleteEvent: vi.fn(), batchDeleteEvents: vi.fn(), previewDelete: vi.fn(), deleteEventsByFilter: vi.fn(), listGroups: vi.fn(),
  showSuccess: vi.fn(), showError: vi.fn(),
}))

vi.mock('../api', () => ({ default: mocks }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: mocks.showSuccess, showError: mocks.showError }) }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ locale: { value: 'en' }, t: (key: string, params?: Record<string, unknown>) => key.replace(/\{(\w+)\}/g, (_, token) => String(params?.[token] ?? `{${token}}`)) }) }
})

const baseConfig = (): PromptAuditConfig => ({
  enabled: true, blocking_enabled: false, blocking_latest_turn_only: false, store_pass_events: false, effective_mode: 'async_audit', strategy: 'priority',
  worker_count: 4, queue_capacity: 100, scanners: SCANNER_CATALOG.map((item) => item.id), all_groups: true, group_ids: [],
  endpoints: [{ id: 'guard-1', name: 'Guard One', protocol: 'openai_compatible', base_url: 'http://127.0.0.1:8000', model: 'guard-model', timeout_ms: 3000, input_limit: 4000, enabled: true, has_token: true, token_status: 'configured' }],
  config_version: 7, updated_at: '2026-07-16T00:00:00Z', updated_by: 1, change_summary: '{}',
})
const runtime = (): PromptAuditRuntime => ({
  process_status: 'running', effective_mode: 'async_audit', expected_config_version: 7, active_config_version: 7,
  worker_total: 4, worker_active: 1, queue_capacity: 100,
  queue: { staging: 0, queued: 0, processing: 1, retry: 0, done: 5, failed: 0, active: 1 },
  processed_total: 5, failed_total: 0, enqueued_total: 5, dropped_total: 0, database_status: 'ok', redis_status: 'ok', endpoints: {},
  guard_metrics: { total: 1, allowed: 1, flagged: 0, blocked: 0, unavailable: 0, invalid: 0, timeouts: 0, failovers: 0, bulkhead_full: 0, record_failed: 0 },
})

const AppLayoutStub = { template: '<div><slot /></div>' }
const RuntimeStub = defineComponent({ props: ['runtime', 'loading', 'error'], emits: ['refresh'], template: '<div data-test="runtime">{{ error }}</div>' })
const EndpointStub = defineComponent({
  props: ['endpoints', 'probeResults', 'probingIds'], emits: ['update:endpoints', 'probe'],
  template: '<div data-test="endpoint"><button data-test="inject-secret" @click="$emit(\'update:endpoints\', endpoints.map((e) => ({ ...e, token: \'PROMPT_AUDIT_CANARY_SECRET_DO_NOT_PERSIST\' })))">secret</button><button data-test="probe" @click="$emit(\'probe\', endpoints[0])">probe</button></div>',
})
const PolicyStub = defineComponent({ props: ['draft', 'groups'], emits: ['update:draft'], template: '<div data-test="policy" />' })
const EventsStub = defineComponent({
  props: ['events', 'filters', 'selectedIds', 'loading', 'error', 'total', 'page', 'pageSize'],
  emits: ['filters-change', 'search', 'selection', 'page', 'page-size', 'view', 'delete', 'batch-delete', 'preview-delete'],
  template: '<div data-test="events"><button data-test="preview" @click="$emit(\'preview-delete\')">preview</button><button data-test="change-filter" @click="$emit(\'filters-change\', { ...filters, keyword: \'changed\' })">change</button><button data-test="delete-one" @click="$emit(\'delete\', 5)">delete</button><button data-test="select-batch" @click="$emit(\'selection\', [5, 6])">select</button><button data-test="delete-batch" @click="$emit(\'batch-delete\')">batch</button></div>',
})
const DetailStub = defineComponent({ props: ['show', 'event', 'loading'], emits: ['close'], template: '<div data-test="detail" />' })
const ConfirmStub = defineComponent({ props: ['show', 'title', 'message'], emits: ['confirm', 'cancel'], template: '<div v-if="show" data-test="confirm"><button data-test="confirm-action" @click="$emit(\'confirm\')">confirm</button></div>' })
const FilterDeleteStub = defineComponent({
  props: ['show', 'initialFilters', 'preview', 'previewing', 'deleting'],
  emits: ['close', 'preview', 'confirm', 'criteria-change'],
  template: '<div v-if="show" data-test="filter-delete-dialog"><button data-test="dialog-preview" @click="$emit(\'preview\', { ...initialFilters, start_at: \'2026-07-15T00:00\', end_at: \'2026-07-16T00:00\' })">run</button><button data-test="dialog-confirm" @click="$emit(\'confirm\', { ...initialFilters, start_at: \'2026-07-15T00:00\', end_at: \'2026-07-16T00:00\' })">confirm</button><span data-test="dialog-preview-state">{{ preview ? preview.matched_count : \'none\' }}</span></div>',
})

function mountView() {
  return mount(PromptAuditView, {
    global: { stubs: { AppLayout: AppLayoutStub, RuntimeOverview: RuntimeStub, EndpointPool: EndpointStub, PolicyPanel: PolicyStub, EventWorkspace: EventsStub, EventDetailDialog: DetailStub, FilterDeleteDialog: FilterDeleteStub, ConfirmDialog: ConfirmStub } },
  })
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail })
  return { promise, resolve, reject }
}

const deletionCriteria = (keyword: string) => ({
  ...emptyEventFilters(), keyword, start_at: '2026-07-01T00:00', end_at: '2026-07-02T00:00',
  api_key_id: '23', request_id: 'request-fixture', prompt_hash: 'f'.repeat(64),
})
const deletionPreview = (token: string): PromptDeletePreview => ({
  matched_count: 2, filter_summary: {}, snapshot_max_id: token === 'old' ? 10 : 20,
  filter_hash: (token === 'old' ? 'a' : 'b').repeat(64), confirmation_token: token,
  expires_at: '2026-07-16T00:05:00Z',
})

const eventPage = (page: number, id: number) => ({ items: [{ id } as PromptAuditEvent], total: 3, page, page_size: 20, pages: 3 })

function draftFrom(wrapper: ReturnType<typeof mountView>): PromptAuditDraft {
  return JSON.parse(JSON.stringify(wrapper.getComponent(PolicyStub).props('draft')))
}
async function editDraft(wrapper: ReturnType<typeof mountView>, change: (draft: PromptAuditDraft) => void) {
  const draft = draftFrom(wrapper)
  change(draft)
  wrapper.getComponent(PolicyStub).vm.$emit('update:draft', draft)
  await flushPromises()
}

describe('PromptAuditView', () => {
  beforeEach(() => {
    Object.values(mocks).forEach((mock) => mock.mockReset())
    mocks.getConfig.mockResolvedValue(baseConfig())
    mocks.getRuntime.mockResolvedValue(runtime())
    mocks.listGroups.mockResolvedValue([])
    mocks.listEvents.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 0 })
    mocks.updateConfig.mockImplementation(async () => ({ ...baseConfig(), config_version: 8 }))
    mocks.probeEndpoint.mockResolvedValue({ ok: true, status: 'healthy', message: 'ok', latency_ms: 2, http_status: 200, retryable: false, checked_at: '2026-07-16T00:00:00Z', token_applied: true })
    mocks.previewDelete.mockResolvedValue({ matched_count: 2, filter_summary: {}, snapshot_max_id: 10, filter_hash: 'a'.repeat(64), confirmation_token: 'opaque-confirmation', expires_at: '2026-07-16T00:05:00Z' })
    mocks.deleteEventsByFilter.mockResolvedValue({ deleted_events: 2, deleted_jobs: 2 })
    mocks.deleteEvent.mockResolvedValue({ deleted_events: 1, deleted_jobs: 1 })
    mocks.batchDeleteEvents.mockResolvedValue({ deleted_events: 2, deleted_jobs: 2 })
  })

  it('starts config, runtime, groups, and events loads independently', async () => {
    mocks.getRuntime.mockRejectedValue(new Error('runtime offline'))
    const wrapper = mountView()
    expect(mocks.getConfig).toHaveBeenCalledOnce()
    expect(mocks.getRuntime).toHaveBeenCalledOnce()
    expect(mocks.listGroups).toHaveBeenCalledOnce()
    expect(mocks.listEvents).toHaveBeenCalledOnce()
    await flushPromises()
    expect(wrapper.get('[data-test="runtime"]').text()).toContain('runtime offline')
    expect(wrapper.find('[data-test="endpoint"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="events"]').exists()).toBe(true)
  })

  it('separates configuration and audit events into page tabs', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-test="tab-events"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.get('[data-test="tab-config"]').attributes('aria-selected')).toBe('false')
    expect(wrapper.get('[data-test="tab-panel-events"]').attributes('style') || '').not.toContain('display: none')
    expect(wrapper.get('[data-test="tab-panel-config"]').attributes('style') || '').toContain('display: none')
    expect(wrapper.find('[data-test="save-config"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="events"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="pass-events-disabled-notice"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="tab-events"]').text()).toContain('admin.promptAudit.tabs.events')
    expect(wrapper.get('[data-test="tab-config"]').text()).toContain('admin.promptAudit.tabs.config')

    await wrapper.get('[data-test="tab-config"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="tab-config"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.get('[data-test="tab-panel-config"]').attributes('style') || '').not.toContain('display: none')
    expect(wrapper.get('[data-test="tab-panel-events"]').attributes('style') || '').toContain('display: none')
    expect(wrapper.find('[data-test="save-config"]').exists()).toBe(true)

    await wrapper.get('[data-test="tab-events"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="tab-events"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.find('[data-test="save-config"]').exists()).toBe(false)

    await wrapper.get('[data-test="pass-events-disabled-notice"] button').trigger('click')
    expect(wrapper.get('[data-test="tab-config"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.find('[data-test="save-config"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="tab-panel-config"]').attributes('style') || '').not.toContain('display: none')
  })

  it('requires confirmation for blocking and disables it when audit is turned off', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="tab-config"]').trigger('click')
    await wrapper.get('[data-test="blocking-toggle"]').trigger('click')
    expect(wrapper.find('[data-test="confirm"]').exists()).toBe(true)
    await wrapper.get('[data-test="confirm-action"]').trigger('click')
    expect(wrapper.get('[data-test="blocking-toggle"]').attributes('aria-checked')).toBe('true')
    await wrapper.get('[data-test="blocking-latest-turn-only-toggle"]').trigger('click')
    expect(wrapper.get('[data-test="blocking-latest-turn-only-toggle"]').attributes('aria-checked')).toBe('true')
    await wrapper.get('[data-test="enabled-toggle"]').trigger('click')
    expect(wrapper.get('[data-test="enabled-toggle"]').attributes('aria-checked')).toBe('false')
    expect(wrapper.get('[data-test="blocking-toggle"]').attributes('aria-checked')).toBe('false')
    expect(wrapper.get('[data-test="blocking-toggle"]').attributes()).toHaveProperty('disabled')
    expect(wrapper.get('[data-test="blocking-latest-turn-only-toggle"]').attributes()).toHaveProperty('disabled')
  })

  it('clears plaintext token state after a successful save', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="tab-config"]').trigger('click')
    await wrapper.get('[data-test="inject-secret"]').trigger('click')
    expect(wrapper.text()).toContain('admin.promptAudit.saveBar.dirty')
    await wrapper.get('[data-test="save-config"]').trigger('click')
    await flushPromises()
    expect(mocks.updateConfig).toHaveBeenCalledWith(expect.objectContaining({ endpoints: [expect.objectContaining({ token: 'PROMPT_AUDIT_CANARY_SECRET_DO_NOT_PERSIST' })] }))
    const endpointProps = wrapper.getComponent(EndpointStub).props('endpoints') as Array<{ token: string }>
    expect(endpointProps[0].token).toBe('')
    expect(wrapper.html()).not.toContain('PROMPT_AUDIT_CANARY_SECRET_DO_NOT_PERSIST')
  })

  it('reports real probe progress/results and invalidates filter confirmation when filters change', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="tab-config"]').trigger('click')
    await wrapper.get('[data-test="probe"]').trigger('click')
    await flushPromises()
    expect(mocks.probeEndpoint).toHaveBeenCalledOnce()
    expect((wrapper.getComponent(EndpointStub).props('probeResults') as Record<string, unknown>)).toHaveProperty('guard-1')

    await wrapper.get('[data-test="tab-events"]').trigger('click')
    await wrapper.get('[data-test="preview"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="filter-delete-dialog"]').exists()).toBe(true)
    expect(mocks.previewDelete).not.toHaveBeenCalled()
    await wrapper.get('[data-test="dialog-preview"]').trigger('click')
    await flushPromises()
    expect(mocks.previewDelete).toHaveBeenCalledOnce()
    expect(wrapper.get('[data-test="dialog-preview-state"]').text()).toBe('2')
    await wrapper.get('[data-test="change-filter"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="filter-delete-dialog"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="dialog-preview-state"]').text()).toBe('none')
  })

  it('uses native labeled switches and a responsive sticky save surface', async () => {
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.find('div.sticky.bottom-0').exists()).toBe(false)
    await wrapper.get('[data-test="tab-config"]').trigger('click')
    const switches = wrapper.findAll('[role="switch"]')
    expect(switches).toHaveLength(4)
    expect(switches.every((item) => Boolean(item.attributes('aria-label')))).toBe(true)
    const saveSurface = wrapper.get('div.sticky.bottom-0')
    expect(saveSurface.classes()).not.toContain('fixed')
    expect(wrapper.get('[data-test="save-config"]').element.closest('.sticky')).toBe(saveSurface.element)
    expect(saveSurface.find('div.flex-wrap').exists()).toBe(true)
  })

  it('executes single, selected-batch, and preview-confirmed filter deletion flows', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-test="delete-one"]').trigger('click')
    await wrapper.get('[data-test="confirm-action"]').trigger('click')
    await flushPromises()
    expect(mocks.deleteEvent).toHaveBeenCalledWith(5)

    await wrapper.get('[data-test="select-batch"]').trigger('click')
    await wrapper.get('[data-test="delete-batch"]').trigger('click')
    await wrapper.get('[data-test="confirm-action"]').trigger('click')
    await flushPromises()
    expect(mocks.batchDeleteEvents).toHaveBeenCalledWith([5, 6])

    await wrapper.get('[data-test="preview"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="dialog-preview"]').trigger('click')
    await flushPromises()
    expect(mocks.previewDelete).toHaveBeenCalledWith(expect.objectContaining({ start_at: '2026-07-15T00:00', end_at: '2026-07-16T00:00' }))
    await wrapper.get('[data-test="dialog-confirm"]').trigger('click')
    await flushPromises()
    expect(mocks.deleteEventsByFilter).toHaveBeenCalledWith(expect.objectContaining({
      start_at: '2026-07-15T00:00',
      end_at: '2026-07-16T00:00',
    }), expect.objectContaining({
      snapshot_max_id: 10,
      confirmation_token: 'opaque-confirmation',
    }))
    expect(wrapper.find('[data-test="filter-delete-dialog"]').exists()).toBe(false)
  })

  it('mints the confirmation token on the fly for one-click filter deletion without a manual preview', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-test="preview"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="filter-delete-dialog"]').exists()).toBe(true)
    expect(mocks.previewDelete).not.toHaveBeenCalled()

    await wrapper.get('[data-test="dialog-confirm"]').trigger('click')
    await flushPromises()
    expect(mocks.previewDelete).toHaveBeenCalledOnce()
    expect(mocks.previewDelete).toHaveBeenCalledWith(expect.objectContaining({ start_at: '2026-07-15T00:00', end_at: '2026-07-16T00:00' }))
    expect(mocks.deleteEventsByFilter).toHaveBeenCalledWith(expect.objectContaining({
      start_at: '2026-07-15T00:00',
      end_at: '2026-07-16T00:00',
    }), expect.objectContaining({
      snapshot_max_id: 10,
      confirmation_token: 'opaque-confirmation',
    }))
    expect(wrapper.find('[data-test="filter-delete-dialog"]').exists()).toBe(false)
  })

  describe('filter deletion generation', () => {
    it('discards a late preview after criteria changes and confirms only the new full filter', async () => {
      const old = deferred<PromptDeletePreview>()
      mocks.previewDelete.mockReturnValueOnce(old.promise).mockResolvedValueOnce(deletionPreview('new'))
      const wrapper = mountView(); await flushPromises()
      await wrapper.get('[data-test="preview"]').trigger('click')
      const dialog = wrapper.getComponent(FilterDeleteStub)
      dialog.vm.$emit('preview', deletionCriteria('old')); await flushPromises()
      dialog.vm.$emit('criteria-change'); await flushPromises()
      old.resolve(deletionPreview('old')); await flushPromises()

      expect(dialog.props('preview')).toBeNull()
      dialog.vm.$emit('confirm', deletionCriteria('new')); await flushPromises()
      expect(mocks.previewDelete).toHaveBeenCalledTimes(2)
      expect(mocks.previewDelete).toHaveBeenLastCalledWith(deletionCriteria('new'))
      expect(mocks.deleteEventsByFilter).toHaveBeenCalledTimes(1)
      expect(mocks.deleteEventsByFilter).toHaveBeenCalledWith(deletionCriteria('new'), deletionPreview('new'))
      wrapper.unmount()
    })

    it.each(['success', 'error'])('ignores late preview %s and finalization after closing and starting a new preview', async (outcome) => {
      const old = deferred<PromptDeletePreview>(), current = deferred<PromptDeletePreview>()
      mocks.previewDelete.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
      const wrapper = mountView(); await flushPromises()
      await wrapper.get('[data-test="preview"]').trigger('click')
      const dialog = wrapper.getComponent(FilterDeleteStub)
      dialog.vm.$emit('preview', deletionCriteria('old')); await flushPromises()
      dialog.vm.$emit('close'); await flushPromises()
      await wrapper.get('[data-test="preview"]').trigger('click')
      dialog.vm.$emit('preview', deletionCriteria('new')); await flushPromises()
      if (outcome === 'success') old.resolve(deletionPreview('old'))
      else old.reject(new Error('superseded preview'))
      await flushPromises()

      expect(dialog.props('preview')).toBeNull()
      expect(dialog.props('previewing')).toBe(true)
      expect(mocks.showError).not.toHaveBeenCalled()
      current.resolve(deletionPreview('new')); await flushPromises()
      dialog.vm.$emit('confirm', deletionCriteria('new')); await flushPromises()
      expect(mocks.previewDelete).toHaveBeenCalledTimes(2)
      expect(mocks.deleteEventsByFilter).toHaveBeenCalledTimes(1)
      expect(mocks.deleteEventsByFilter).toHaveBeenCalledWith(deletionCriteria('new'), deletionPreview('new'))
      wrapper.unmount()
    })

    it.each(['criteria-change', 'close'])('does not submit a one-click deletion after %s invalidates its pending preview', async (invalidate) => {
      const preview = deferred<PromptDeletePreview>()
      mocks.previewDelete.mockReturnValueOnce(preview.promise)
      const wrapper = mountView(); await flushPromises()
      await wrapper.get('[data-test="preview"]').trigger('click')
      const dialog = wrapper.getComponent(FilterDeleteStub)
      dialog.vm.$emit('confirm', deletionCriteria('old')); await flushPromises()
      dialog.vm.$emit(invalidate); await flushPromises()
      preview.resolve(deletionPreview('old')); await flushPromises()

      expect(mocks.deleteEventsByFilter).toHaveBeenCalledTimes(0)
      expect(mocks.showSuccess).not.toHaveBeenCalled()
      expect(dialog.props('deleting')).toBe(false)
      wrapper.unmount()
    })

    it('reports an already submitted deletion without clearing a newer confirmation preview', async () => {
      const deletion = deferred<{ deleted_events: number; deleted_jobs: number }>()
      mocks.previewDelete.mockResolvedValueOnce(deletionPreview('old')).mockResolvedValueOnce(deletionPreview('new'))
      mocks.deleteEventsByFilter.mockReturnValueOnce(deletion.promise)
      const wrapper = mountView(); await flushPromises()
      await wrapper.get('[data-test="preview"]').trigger('click')
      const dialog = wrapper.getComponent(FilterDeleteStub)
      dialog.vm.$emit('preview', deletionCriteria('old')); await flushPromises()
      dialog.vm.$emit('confirm', deletionCriteria('old')); await flushPromises()
      expect(mocks.deleteEventsByFilter).toHaveBeenCalledTimes(1)
      dialog.vm.$emit('close'); await flushPromises()
      await wrapper.get('[data-test="preview"]').trigger('click')
      dialog.vm.$emit('preview', deletionCriteria('new')); await flushPromises()
      deletion.resolve({ deleted_events: 2, deleted_jobs: 2 }); await flushPromises()

      expect(mocks.showSuccess).toHaveBeenCalledWith('admin.promptAudit.messages.deleted')
      expect(dialog.props('show')).toBe(true)
      expect(dialog.props('preview')).toEqual(deletionPreview('new'))
      expect(mocks.deleteEventsByFilter).toHaveBeenCalledTimes(1)
      wrapper.unmount()
    })
  })

  describe('save response reconciliation', () => {
    it('preserves later fields and new tokens while retiring only sent credential actions', async () => {
      const config = baseConfig()
      config.endpoints = ['guard-1', 'guard-2', 'guard-3'].map(id => ({ ...config.endpoints[0], id, name: id }))
      mocks.getConfig.mockResolvedValue(config)
      const save = deferred<PromptAuditConfig>()
      mocks.updateConfig.mockReturnValueOnce(save.promise)
      const wrapper = mountView(); await flushPromises()
      await wrapper.get('[data-test="tab-config"]').trigger('click')
      await editDraft(wrapper, draft => {
        draft.worker_count = 6
        draft.endpoints[0].token = 'sent-token-one'
        draft.endpoints[1].clear_token = true
        draft.endpoints[2].token = 'sent-token-three'
      })
      await wrapper.get('[data-test="save-config"]').trigger('click')
      await editDraft(wrapper, draft => {
        draft.worker_count = 9
        draft.endpoints[0].token = 'later-token-one'
        draft.endpoints[1].token = 'later-token-two'
        draft.endpoints[1].clear_token = false
        draft.endpoints.reverse()
      })
      expect(mocks.updateConfig.mock.calls[0][0]).toMatchObject({
        expected_config_version: 7, worker_count: 6,
        endpoints: [expect.objectContaining({ token: 'sent-token-one' }), expect.objectContaining({ clear_token: true }), expect.objectContaining({ token: 'sent-token-three' })],
      })
      const saved = { ...config, worker_count: 6, config_version: 8,
        endpoints: config.endpoints.map(endpoint => endpoint.id === 'guard-2' ? { ...endpoint, has_token: false, token_status: 'missing' } : endpoint) }
      save.resolve(saved); await flushPromises()

      const current = draftFrom(wrapper)
      expect(current).toMatchObject({ worker_count: 9, config_version: 8 })
      expect(current.endpoints.map(endpoint => endpoint.id)).toEqual(['guard-3', 'guard-2', 'guard-1'])
      expect(current.endpoints.map(endpoint => [endpoint.token, endpoint.clear_token])).toEqual([
        ['', false], ['later-token-two', false], ['later-token-one', false],
      ])
      expect(wrapper.text()).toContain('admin.promptAudit.saveBar.dirty')
      expect(mocks.updateConfig).toHaveBeenCalledTimes(1)
      mocks.updateConfig.mockResolvedValueOnce({ ...config, config_version: 9, worker_count: 9, endpoints: [...config.endpoints].reverse() })
      await wrapper.get('[data-test="save-config"]').trigger('click'); await flushPromises()
      expect(mocks.updateConfig).toHaveBeenCalledTimes(2)
      expect(mocks.updateConfig.mock.calls[1][0]).toMatchObject({
        expected_config_version: 8, worker_count: 9,
        endpoints: [expect.objectContaining({ id: 'guard-3', token: undefined, clear_token: false }), expect.objectContaining({ token: 'later-token-two' }), expect.objectContaining({ token: 'later-token-one' })],
      })
      expect(draftFrom(wrapper).endpoints.every(endpoint => endpoint.token === '' && !endpoint.clear_token)).toBe(true)
      expect(wrapper.text()).toContain('admin.promptAudit.saveBar.synced')
      wrapper.unmount()
    })

    it('preserves a later explicit clear without repeating it after its own save succeeds', async () => {
      const save = deferred<PromptAuditConfig>()
      mocks.updateConfig.mockReturnValueOnce(save.promise)
      const wrapper = mountView(); await flushPromises()
      await wrapper.get('[data-test="tab-config"]').trigger('click')
      await editDraft(wrapper, draft => { draft.endpoints[0].token = 'sent-token' })
      await wrapper.get('[data-test="save-config"]').trigger('click')
      await editDraft(wrapper, draft => { draft.endpoints[0].token = ''; draft.endpoints[0].clear_token = true })
      save.resolve({ ...baseConfig(), config_version: 8 }); await flushPromises()

      expect(draftFrom(wrapper).endpoints[0]).toMatchObject({ token: '', clear_token: true })
      mocks.updateConfig.mockResolvedValueOnce({ ...baseConfig(), config_version: 9, endpoints: [{ ...baseConfig().endpoints[0], has_token: false, token_status: 'missing' }] })
      await wrapper.get('[data-test="save-config"]').trigger('click'); await flushPromises()
      expect(mocks.updateConfig.mock.calls[1][0]).toMatchObject({ expected_config_version: 8, endpoints: [expect.objectContaining({ token: undefined, clear_token: true })] })
      expect(draftFrom(wrapper).endpoints[0]).toMatchObject({ token: '', clear_token: false, has_token: false })
      await editDraft(wrapper, draft => { draft.worker_count = 5 })
      await wrapper.get('[data-test="save-config"]').trigger('click'); await flushPromises()
      expect(mocks.updateConfig.mock.calls[2][0]).toMatchObject({ expected_config_version: 9, endpoints: [expect.objectContaining({ token: undefined, clear_token: false })] })
      wrapper.unmount()
    })

    it('does not duplicate an in-flight save and accepts normalization when no later edit exists', async () => {
      const save = deferred<PromptAuditConfig>()
      mocks.updateConfig.mockReturnValue(save.promise)
      const wrapper = mountView(); await flushPromises()
      await wrapper.get('[data-test="tab-config"]').trigger('click')
      await editDraft(wrapper, draft => { draft.endpoints[0].name = '  Normalized  '; draft.endpoints[0].token = 'sent-token' })
      const button = wrapper.get('[data-test="save-config"]').element
      button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await flushPromises()
      expect(mocks.updateConfig).toHaveBeenCalledTimes(1)
      expect(mocks.updateConfig.mock.calls[0][0]).toMatchObject({ expected_config_version: 7, endpoints: [expect.objectContaining({ name: 'Normalized' })] })
      save.resolve({ ...baseConfig(), config_version: 8, endpoints: [{ ...baseConfig().endpoints[0], name: 'Normalized' }] }); await flushPromises()
      expect(draftFrom(wrapper).endpoints[0]).toMatchObject({ name: 'Normalized', token: '', clear_token: false })
      expect(wrapper.text()).toContain('admin.promptAudit.saveBar.synced')
      wrapper.unmount()
    })

    it('keeps failed-save drafts and their original CAS version for an explicit retry', async () => {
      mocks.updateConfig.mockRejectedValueOnce({ code: 'prompt_audit_config_conflict', message: 'fixture conflict' })
      const wrapper = mountView(); await flushPromises()
      await wrapper.get('[data-test="tab-config"]').trigger('click')
      await editDraft(wrapper, draft => { draft.queue_capacity = 123; draft.endpoints[0].token = 'retry-token' })
      await wrapper.get('[data-test="save-config"]').trigger('click'); await flushPromises()
      expect(draftFrom(wrapper)).toMatchObject({ config_version: 7, queue_capacity: 123, endpoints: [expect.objectContaining({ token: 'retry-token' })] })
      expect(wrapper.text()).toContain('admin.promptAudit.saveBar.dirty')
      expect(mocks.updateConfig).toHaveBeenCalledTimes(1)
      expect(mocks.getConfig).toHaveBeenCalledTimes(1)
      expect(mocks.showSuccess).not.toHaveBeenCalled()
      expect(mocks.showError).toHaveBeenCalledTimes(1)
      wrapper.unmount()
    })

    it('reports a submitted save after unmount without starting another runtime request', async () => {
      const save = deferred<PromptAuditConfig>()
      mocks.updateConfig.mockReturnValueOnce(save.promise)
      const wrapper = mountView(); await flushPromises()
      await wrapper.get('[data-test="tab-config"]').trigger('click')
      await editDraft(wrapper, draft => { draft.worker_count = 5 })
      await wrapper.get('[data-test="save-config"]').trigger('click')
      wrapper.unmount()
      save.resolve({ ...baseConfig(), config_version: 8, worker_count: 5 }); await flushPromises()
      expect(mocks.updateConfig).toHaveBeenCalledTimes(1)
      expect(mocks.showSuccess).toHaveBeenCalledWith('admin.promptAudit.messages.saved')
      expect(mocks.getRuntime).toHaveBeenCalledTimes(1)
    })
  })

  describe('read request generations', () => {
    it('keeps the newer event page and selection when an older list response arrives last', async () => {
      const wrapper = mountView(); await flushPromises()
      const old = deferred<ReturnType<typeof eventPage>>(), current = deferred<ReturnType<typeof eventPage>>()
      mocks.listEvents.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
      const workspace = wrapper.getComponent(EventsStub)
      workspace.vm.$emit('page', 2); await flushPromises()
      workspace.vm.$emit('page', 3); await flushPromises()
      current.resolve(eventPage(3, 30)); await flushPromises()
      workspace.vm.$emit('selection', [30]); await flushPromises()
      old.resolve(eventPage(2, 20)); await flushPromises()
      expect(workspace.props()).toMatchObject({ page: 3, events: [{ id: 30 }], selectedIds: [30], loading: false, error: '' })
      wrapper.unmount()
    })

    it('ignores an older list error and finally while the newer request is still loading', async () => {
      const wrapper = mountView(); await flushPromises()
      const old = deferred<ReturnType<typeof eventPage>>(), current = deferred<ReturnType<typeof eventPage>>()
      mocks.listEvents.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
      const workspace = wrapper.getComponent(EventsStub)
      workspace.vm.$emit('search', { ...emptyEventFilters(), keyword: 'old' }); await flushPromises()
      workspace.vm.$emit('search', { ...emptyEventFilters(), keyword: 'current' }); await flushPromises()
      old.reject(new Error('old list error')); await flushPromises()
      expect(workspace.props()).toMatchObject({ loading: true, error: '' })
      current.reject(new Error('current list error')); await flushPromises()
      expect(workspace.props()).toMatchObject({ loading: false, error: 'current list error' })
      expect(mocks.showError).not.toHaveBeenCalled()
      wrapper.unmount()
    })

    it('keeps the newer detail when an older detail succeeds last', async () => {
      const wrapper = mountView(); await flushPromises()
      const old = deferred<PromptAuditEvent>(), current = deferred<PromptAuditEvent>()
      mocks.getEvent.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
      const workspace = wrapper.getComponent(EventsStub), detail = wrapper.getComponent(DetailStub)
      workspace.vm.$emit('view', 10); await flushPromises()
      workspace.vm.$emit('view', 20); await flushPromises()
      current.resolve({ id: 20 } as PromptAuditEvent); await flushPromises()
      old.resolve({ id: 10 } as PromptAuditEvent); await flushPromises()
      expect(detail.props()).toMatchObject({ show: true, event: { id: 20 }, loading: false })
      wrapper.unmount()
    })

    it('ignores an older detail error and finally while the newer detail is loading', async () => {
      const wrapper = mountView(); await flushPromises()
      const old = deferred<PromptAuditEvent>(), current = deferred<PromptAuditEvent>()
      mocks.getEvent.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
      const workspace = wrapper.getComponent(EventsStub), detail = wrapper.getComponent(DetailStub)
      workspace.vm.$emit('view', 10); await flushPromises()
      workspace.vm.$emit('view', 20); await flushPromises()
      old.reject(new Error('old detail error')); await flushPromises()
      expect(detail.props()).toMatchObject({ show: true, event: null, loading: true })
      expect(mocks.showError).not.toHaveBeenCalled()
      current.resolve({ id: 20 } as PromptAuditEvent); await flushPromises()
      expect(detail.props()).toMatchObject({ show: true, event: { id: 20 }, loading: false })
      wrapper.unmount()
    })

    it.each(['success', 'error'])('discards a closed detail request %s without restoring its state or warning', async (outcome) => {
      const wrapper = mountView(); await flushPromises()
      const request = deferred<PromptAuditEvent>()
      mocks.getEvent.mockReturnValueOnce(request.promise)
      wrapper.getComponent(EventsStub).vm.$emit('view', 10); await flushPromises()
      const detail = wrapper.getComponent(DetailStub)
      detail.vm.$emit('close'); await flushPromises()
      if (outcome === 'success') request.resolve({ id: 10 } as PromptAuditEvent)
      else request.reject(new Error('closed detail error'))
      await flushPromises()
      expect(detail.props()).toMatchObject({ show: false, event: null, loading: false })
      expect(mocks.showError).not.toHaveBeenCalled()
      wrapper.unmount()
    })

    it('drops pending read callbacks on unmount without triggering further calls or errors', async () => {
      const wrapper = mountView(); await flushPromises()
      const list = deferred<ReturnType<typeof eventPage>>(), detail = deferred<PromptAuditEvent>()
      mocks.listEvents.mockReturnValueOnce(list.promise)
      mocks.getEvent.mockReturnValueOnce(detail.promise)
      const workspace = wrapper.getComponent(EventsStub)
      workspace.vm.$emit('page', 2); workspace.vm.$emit('view', 10); await flushPromises()
      wrapper.unmount()
      list.reject(new Error('closed list')); detail.reject(new Error('closed detail')); await flushPromises()
      expect(mocks.showError).not.toHaveBeenCalled()
      expect(mocks.listEvents).toHaveBeenCalledTimes(2)
      expect(mocks.getEvent).toHaveBeenCalledTimes(1)
    })
  })
})
