import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import type { CapabilityCandidate, CapabilityChangeset, CapabilityItem, CapabilityRun, CapabilityScopeAccount } from '@/api/admin/accountCapabilities'

const { api, listFolders, getAllIncludingInactive, routeQuery } = vi.hoisted(() => ({
  api: { overview: vi.fn(), candidates: vi.fn(), inventory: vi.fn(), listRuns: vi.fn(), getRun: vi.fn(), listItems: vi.fn(), createRun: vi.fn(), controlRun: vi.fn(), preview: vi.fn(), getChangeset: vi.fn(), apply: vi.fn() },
  listFolders: vi.fn(), getAllIncludingInactive: vi.fn(), routeQuery: {} as Record<string, string>,
}))
vi.mock('@/api/admin/accountCapabilities', () => ({ default: api }))
vi.mock('@/api/admin/accounts', () => ({ listFolders, default: { listFolders } }))
vi.mock('@/api/admin/groups', () => ({ getAllIncludingInactive, default: { getAllIncludingInactive } }))
vi.mock('vue-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-router')>()
  return { ...actual, useRoute: () => ({ query: routeQuery }) }
})
vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return { ...actual, useI18n: () => ({ t: (key: string, values?: Record<string, unknown>) => values ? `${key} ${JSON.stringify(values)}` : key, te: () => true }) }
})
import router from '@/router'
import AccountCapabilitiesView from '../AccountCapabilitiesView.vue'

const scopeAccounts: CapabilityScopeAccount[] = [
  { id: 71, name: 'DMX', folder_id: 17, platform: 'openai', status: 'active', schedulable: false },
  { id: 72, name: 'Shared', folder_id: 28, platform: 'openai', status: 'active', schedulable: true },
  // A source account with no mainstream candidate must still be frozen into
  // discovery and publication scope, not silently omitted by pagination.
  { id: 73, name: 'No current candidate', folder_id: 28, platform: 'openai', status: 'active', schedulable: false },
]
const candidate: CapabilityCandidate = {
  candidate_id: 'candidate-1', account_id: 71, account_name: 'DMX', folder_id: 17,
  public_model: 'gpt-6-astra', upstream_model: 'ns/GPT-6-Astra-ssvip', aliases: [],
  protocol: 'responses', profile: 'text', tier: 'ssvip', group_name: 'gpt-vip', group_id: 4,
  discovered: true, configured: false, published: false, discovery_status: 'available',
  latest_probe_item_id: 41, probe_status: 'alive', stale: false, warnings: [],
}
const run: CapabilityRun = { id: 11, created_by: 1, kind: 'probe', folder_ids: [17, 28], account_ids: [71], status: 'completed', target_count: 1, processed_count: 1, succeeded_count: 1, failed_count: 0, request_count: 1, possibly_sent_count: 0, created_at: '2026-09-08T00:00:00Z', updated_at: '2026-09-08T00:00:01Z' }
const item: CapabilityItem = { id: 41, run_id: 11, account_id: 71, account_name: 'DMX', folder_id: 17, upstream_model: 'ns/GPT-6-Astra-ssvip', protocol: 'responses', profile: 'text', aliases: ['gpt-6-astra'], status: 'succeeded', request_count: 1, stale_config: false, is_current_scope: true, result: { status: 'alive', latency_ms: 20, classification: 'valid_output' } }
const changeset: CapabilityChangeset = { id: 9, scope: { folder_ids: [17, 28], account_ids: [71, 72, 73] }, status: 'preview', created_at: '2026-09-08T00:00:00Z', changes: [{ kind: 'scheduling', account_id: 71, label: 'DMX scheduling', before: false, after: true, evidence_ids: [41] }], warnings: ['Global scheduling also affects private groups.'] }
const page = <T,>(items: T[]) => ({ items, total: items.length, page: 1, page_size: 20 })
const wrappers: VueWrapper[] = []

function renderView(): VueWrapper {
  const wrapper = mount(AccountCapabilitiesView, { global: { stubs: {
    AppLayout: { template: '<div><slot /></div>' },
    TablePageLayout: { template: '<div><slot name="filters"/><slot name="table"/><slot name="pagination"/></div>' },
    BaseDialog: { name: 'CapabilityDialogStub', props: ['show', 'title'], emits: ['close'], template: '<section v-if="show" class="test-dialog"><h2>{{ title }}</h2><slot/><slot name="footer"/></section>' },
    DataTable: { name: 'CapabilityTableStub', props: ['data', 'columns', 'selectedKeys', 'rowKey'], emits: ['update:selectedKeys'], template: '<div class="test-table"><article v-for="row in data" :key="row[rowKey]"><div v-for="column in columns" :key="column.key"><slot :name="`cell-${column.key}`" :row="row"/></div></article></div>' },
    Pagination: { name: 'PaginationStub', props: ['page', 'total', 'pageSize'], emits: ['update:page', 'update:pageSize'], template: '<div class="test-pagination"/>' },
    Icon: true,
  } } })
  wrappers.push(wrapper)
  return wrapper
}
function table(wrapper: VueWrapper, index = 0) { return wrapper.findAllComponents({ name: 'CapabilityTableStub' })[index] }
async function selectCandidates(wrapper: VueWrapper, keys = ['candidate-1']) { table(wrapper).vm.$emit('update:selectedKeys', keys); await flushPromises() }

describe('AccountCapabilitiesView', () => {
  beforeEach(() => {
    vi.clearAllMocks(); setActivePinia(createPinia())
    for (const key of Object.keys(routeQuery)) delete routeQuery[key]
    // These regression cases exercise the explicitly chosen advanced tools.
    // The default overview has its own beginner-flow component tests.
    routeQuery.tab = 'inventory'
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
    listFolders.mockResolvedValue([{ id: 17, name: 'dmxapi', account_count: 1 }, { id: 28, name: '白嫖', account_count: 2 }, { id: 99, name: 'Private', account_count: 1 }])
    getAllIncludingInactive.mockResolvedValue([{ id: 3, name: 'gpt', rate_multiplier: 0.2, platform: 'openai' }, { id: 4, name: 'gpt-vip', rate_multiplier: 0.3, platform: 'openai' }, { id: 5, name: 'claude(非逆向渠道)', rate_multiplier: 0.5, platform: 'anthropic' }])
    api.candidates.mockResolvedValue({ ...page([candidate]), accounts: scopeAccounts })
    api.overview.mockResolvedValue({ scope: { folder_ids: [17, 28], account_ids: [71, 72, 73] }, accounts: scopeAccounts, groups: [], totals: { group_count: 0, published_model_count: 0, verified_account_count: 0, routing_ready_account_count: 0, attention_count: 0 } })
    api.inventory.mockResolvedValue(page([]))
    api.listRuns.mockResolvedValue(page([run]))
    api.getRun.mockResolvedValue(run)
    api.listItems.mockResolvedValue(page([item]))
    api.createRun.mockResolvedValue(run)
    api.controlRun.mockResolvedValue({ ...run, status: 'paused' })
    api.preview.mockResolvedValue(changeset)
    api.getChangeset.mockResolvedValue(changeset)
    api.apply.mockResolvedValue({ ...changeset, status: 'applied' })
  })
  afterEach(() => { for (const wrapper of wrappers.splice(0)) wrapper.unmount(); vi.useRealTimers() })

  it('exposes only an authenticated administrator route', () => {
    expect(router.getRoutes().find((value) => value.path === '/admin/account-capabilities')?.meta).toMatchObject({ requiresAuth: true, requiresAdmin: true })
  })

  it('opens the group overview by default without loading account tables, history or starting model requests', async () => {
    delete routeQuery.tab
    const wrapper = renderView(); await flushPromises()
    expect(wrapper.get('[data-test="capability-tab-overview"]').attributes('aria-selected')).toBe('true')
    expect(api.overview).toHaveBeenCalledTimes(1)
    expect(api.candidates).not.toHaveBeenCalled()
    expect(api.listRuns).not.toHaveBeenCalled()
    expect(api.createRun).not.toHaveBeenCalled()
  })

  it('resolves account-only entry scope without silently intersecting the two default folders', async () => {
    routeQuery.tab = 'overview'; routeQuery.account_ids = '99'
    api.overview.mockResolvedValue({ scope: { folder_ids: [99], account_ids: [99] }, accounts: [{ ...scopeAccounts[0], id: 99, folder_id: 99 }], groups: [], totals: { group_count: 0, published_model_count: 0, verified_account_count: 0, routing_ready_account_count: 0, attention_count: 0 } })
    const wrapper = renderView(); await flushPromises()
    expect(api.overview).toHaveBeenCalledTimes(1)
    expect(api.overview).toHaveBeenCalledWith(expect.objectContaining({ folder_ids: undefined, account_ids: '99' }), expect.any(AbortSignal))
    expect(wrapper.get('[data-test="capability-folder-99"]').element).toHaveProperty('checked', true)
    expect(wrapper.get('[data-test="capability-folder-17"]').element).toHaveProperty('checked', false)
    expect(api.createRun).not.toHaveBeenCalled()
  })

  it.each(['inventory', 'runs'])('reads an account-only advanced deep link in %s without substituting the default folders', async (tab) => {
    routeQuery.tab = tab; routeQuery.account_ids = '99'
    api.candidates.mockResolvedValue({ ...page([]), accounts: [{ ...scopeAccounts[0], id: 99, folder_id: 99 }] })
    const wrapper = renderView(); await flushPromises()
    if (tab === 'inventory') {
      expect(api.candidates).toHaveBeenCalledWith(expect.objectContaining({ folder_ids: undefined, account_ids: '99' }), expect.any(AbortSignal))
      expect(wrapper.get('[data-test="capability-folder-99"]').element).toHaveProperty('checked', true)
    } else {
      expect(api.listRuns).toHaveBeenCalledWith(expect.objectContaining({ folder_ids: undefined, account_ids: '99' }), expect.any(AbortSignal))
    }
    expect(wrapper.get('[data-test="capability-folder-17"]').element).toHaveProperty('checked', false)
    expect(api.createRun).not.toHaveBeenCalled()
    expect(api.preview).not.toHaveBeenCalled()
    expect(api.apply).not.toHaveBeenCalled()
  })

  it('resolves source folders by name, keeps the full scope and does not poll idle history or fetch full catalogs', async () => {
    vi.useFakeTimers()
    const wrapper = renderView(); await flushPromises()
    expect(api.candidates).toHaveBeenCalledWith(expect.objectContaining({ folder_ids: '17,28', page: 1, page_size: 20 }), expect.any(AbortSignal))
    expect(wrapper.get('[data-test="capability-folder-17"]').element).toHaveProperty('checked', true)
    expect(wrapper.get('[data-test="capability-folder-99"]').element).toHaveProperty('checked', false)
    await vi.advanceTimersByTimeAsync(16000)
    expect(api.candidates).toHaveBeenCalledTimes(1)
    expect(api.listRuns).toHaveBeenCalledTimes(1)
    expect(api.inventory).not.toHaveBeenCalled()
    await wrapper.get('[data-test="capability-discover"]').trigger('click'); await flushPromises()
    expect(api.createRun).toHaveBeenCalledWith({ kind: 'discover', folder_ids: [17, 28], account_ids: [71, 72, 73] }, expect.any(String))
  })

  it('uses real server alias candidates, deduplicates exact upstream probes and never auto-submits', async () => {
    api.candidates.mockResolvedValue({ ...page([candidate, { ...candidate, candidate_id: 'alias', public_model: 'documented-alias' }]), accounts: scopeAccounts })
    const wrapper = renderView(); await flushPromises(); await selectCandidates(wrapper, ['candidate-1', 'alias'])
    await wrapper.get('[data-test="capability-probe"]').trigger('click'); await flushPromises()
    expect(wrapper.get('[data-test="capability-dedup-count"]').text()).toContain('"count":1')
    expect(api.createRun).not.toHaveBeenCalled()
    await wrapper.get('[data-test="capability-start-probe"]').trigger('click'); await flushPromises()
    expect(api.createRun.mock.calls[0][0]).toMatchObject({ kind: 'probe', account_ids: [71], items: [{ account_id: 71, upstream_model: 'ns/GPT-6-Astra-ssvip', protocol: 'responses', profile: 'text', aliases: ['gpt-6-astra', 'documented-alias'] }] })
  })

  it('reuses the submission key after an uncertain response instead of creating another billable run', async () => {
    api.createRun.mockRejectedValueOnce(new Error('network')).mockResolvedValueOnce(run)
    const wrapper = renderView(); await flushPromises(); await selectCandidates(wrapper)
    await wrapper.get('[data-test="capability-probe"]').trigger('click'); await wrapper.get('[data-test="capability-start-probe"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-test="capability-start-probe"]').trigger('click'); await flushPromises()
    expect(api.createRun).toHaveBeenCalledTimes(2)
    expect(api.createRun.mock.calls[1]).toEqual(api.createRun.mock.calls[0])
  })

  it('publishes alive SSVIP evidence as VIP and retains every scoped account plus original group identity and rate', async () => {
    const wrapper = renderView(); await flushPromises(); await selectCandidates(wrapper)
    expect(wrapper.get('[data-test="capability-prepare-preview"]').attributes('disabled')).toBeUndefined()
    await wrapper.get('[data-test="capability-prepare-preview"]').trigger('click')
    await wrapper.get('[data-test="capability-generate-preview"]').trigger('click'); await flushPromises()
    expect(api.preview).toHaveBeenCalledWith({ operation: 'merge', idempotency_key: expect.any(String), scope: { folder_ids: [17, 28], account_ids: [71, 72, 73] }, groups: [{ id: 4, name: 'gpt-vip', platform: 'openai', rate_multiplier: 0.3, models: [{ public_model: 'gpt-6-astra', aliases: [], tier: 'vip', evidence_ids: [41] }] }], detach_account_ids: [], scheduling_evidence_ids: [] })
    expect(api.apply).not.toHaveBeenCalled()
    expect(wrapper.get('[data-test="capability-frozen-scope"]').text()).toContain('71, 72, 73')
    await wrapper.get('[data-test="capability-apply"]').trigger('click'); await flushPromises()
    expect(api.apply).toHaveBeenCalledWith(9)
    expect(wrapper.get('[data-test="capability-apply"]').attributes('disabled')).toBeDefined()
  })

  it('never offers stale success evidence for publication and clears selections when sources change', async () => {
    api.candidates.mockResolvedValue({ ...page([{ ...candidate, stale: true }]), accounts: scopeAccounts })
    const wrapper = renderView(); await flushPromises(); await selectCandidates(wrapper)
    expect(wrapper.get('[data-test="capability-prepare-preview"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-test="capability-folder-17"]').setValue(false); await flushPromises()
    expect(wrapper.get('[data-test="capability-probe"]').attributes('disabled')).toBeDefined()
    expect(api.candidates).toHaveBeenLastCalledWith(expect.objectContaining({ folder_ids: '28' }), expect.any(AbortSignal))
  })

  it('loads full catalogs only on explicit expansion and preserves legal empty discovery results', async () => {
    api.inventory.mockResolvedValue(page([{ ...item, upstream_model: '', result: { status: 'empty', models: [] } }]))
    const wrapper = renderView(); await flushPromises()
    await wrapper.get('[data-test="capability-catalog"]').trigger('click'); await flushPromises()
    expect(api.inventory).toHaveBeenCalledWith(expect.objectContaining({ folder_ids: '17,28', kind: 'discover' }), expect.any(AbortSignal))
    expect(wrapper.text()).toContain('empty')
    expect(api.createRun).not.toHaveBeenCalled()
  })

  it('polls visible active work, stops while hidden and refreshes final evidence once before stopping', async () => {
    vi.useFakeTimers()
    api.listRuns.mockResolvedValue(page([{ ...run, status: 'running' }]))
    const wrapper = renderView(); await flushPromises()
    const count = api.listRuns.mock.calls.length
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' }); document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(12000); expect(api.listRuns).toHaveBeenCalledTimes(count)
    api.listRuns.mockResolvedValue(page([run]))
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' }); document.dispatchEvent(new Event('visibilitychange')); await flushPromises()
    const afterCompletion = api.listRuns.mock.calls.length
    expect(api.candidates.mock.calls.length).toBeGreaterThanOrEqual(3)
    await vi.advanceTimersByTimeAsync(12000)
    expect(api.listRuns).toHaveBeenCalledTimes(afterCompletion)
    wrapper.unmount()
  })

  it('pauses via the server and only exposes explicit account-failure scheduling evidence', async () => {
    api.getRun.mockResolvedValue({ ...run, status: 'running' })
    api.listItems.mockResolvedValue(page([
      { ...item, id: 51, status: 'failed', result: { status: 'failed', classification: 'credential_invalid', account_failure: true } },
      { ...item, id: 52, status: 'failed', result: { status: 'failed', classification: 'rate_limited', account_failure: false } },
    ]))
    const wrapper = renderView(); await flushPromises(); await wrapper.get('[data-test="capability-tab-runs"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-test="capability-run-11"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-test="capability-pause"]').trigger('click'); await flushPromises()
    expect(api.controlRun).toHaveBeenCalledWith(11, 'pause')
    expect(wrapper.find('[data-test="capability-resume"]').exists()).toBe(true)
    table(wrapper, 1).vm.$emit('update:selectedKeys', [52]); await flushPromises()
    expect(wrapper.get('[data-test="capability-scheduling-preview"]').attributes('disabled')).toBeDefined()
    table(wrapper, 1).vm.$emit('update:selectedKeys', [51, 52]); await flushPromises()
    await wrapper.get('[data-test="capability-scheduling-preview"]').trigger('click')
    await wrapper.get('[data-test="capability-generate-preview"]').trigger('click'); await flushPromises()
    expect(api.preview).toHaveBeenCalledWith(expect.objectContaining({ scope: { folder_ids: [17, 28], account_ids: [71, 72, 73] }, groups: [], scheduling_evidence_ids: [51] }))
  })

  it('does not revive a preview that was invalidated while a read was in flight', async () => {
    const wrapper = renderView(); await flushPromises(); await selectCandidates(wrapper)
    await wrapper.get('[data-test="capability-prepare-preview"]').trigger('click'); await wrapper.get('[data-test="capability-generate-preview"]').trigger('click'); await flushPromises()
    let finish!: (value: CapabilityChangeset) => void
    api.getChangeset.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve }))
    await wrapper.get('[data-test="capability-refresh"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-test="capability-public-model-0"]').setValue('edited-name')
    finish(changeset); await flushPromises()
    expect(wrapper.find('[data-test="capability-apply"]').exists()).toBe(false)
  })

  it('ignores an older run detail response after the administrator opens a different run', async () => {
    api.listRuns.mockResolvedValue(page([run, { ...run, id: 12 }]))
    let finishOld!: (value: CapabilityRun) => void
    api.getRun.mockImplementationOnce(() => new Promise((resolve) => { finishOld = resolve })).mockResolvedValueOnce({ ...run, id: 12 })
    const wrapper = renderView(); await flushPromises(); await wrapper.get('[data-test="capability-tab-runs"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-test="capability-run-11"]').trigger('click')
    await wrapper.get('[data-test="capability-run-12"]').trigger('click'); await flushPromises()
    finishOld(run); await flushPromises()
    expect(wrapper.get('.test-dialog h2').text()).toContain('"id":12')
    expect(api.listItems).toHaveBeenCalledTimes(1)
    expect(api.listItems).toHaveBeenCalledWith(12, expect.any(Object))
  })

  it('preserves mixed profiles on explicit retest and separates unknown sends from confirmed request counts', async () => {
    api.listItems.mockResolvedValue(page([
      { ...item, id: 61, status: 'indeterminate', profile: 'text', request_count: 0, request_count_unknown: true, result: { status: 'indeterminate' } },
      { ...item, id: 62, profile: 'tool_roundtrip' },
    ]))
    const wrapper = renderView(); await flushPromises(); await wrapper.get('[data-test="capability-tab-runs"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-test="capability-run-11"]').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('admin.accountCapabilities.requestCountUnknown')
    table(wrapper, 1).vm.$emit('update:selectedKeys', [61, 62]); await flushPromises()
    await wrapper.get('[data-test="capability-retest"]').trigger('click'); await flushPromises()
    expect(wrapper.get('[data-test="capability-profile"]').element).toHaveProperty('value', 'original')
    expect(wrapper.get('[data-test="capability-dedup-count"]').text()).toContain('"count":2')
    expect(wrapper.get('[data-test="capability-dedup-count"]').text()).toContain('"requests":3')
    expect(api.createRun).not.toHaveBeenCalled()
  })

  it('catalog race: keeps the newer page when the same-scope previous page resolves late', async () => {
    const catalogItem = (id: number, name: string) => ({ ...item, id, account_name: name, upstream_model: '', result: { status: 'available', models: [] } })
    api.inventory.mockResolvedValueOnce({ ...page([catalogItem(90, 'first-catalog')]), total: 60 })
    const wrapper = renderView(); await flushPromises(); await wrapper.get('[data-test="capability-catalog"]').trigger('click'); await flushPromises()
    let finishOld!: (value: unknown) => void
    api.inventory.mockImplementationOnce(() => new Promise((resolve) => { finishOld = resolve }))
      .mockResolvedValueOnce({ ...page([catalogItem(92, 'newer-catalog')]), total: 60, page: 2 })
    const pagination = wrapper.findAllComponents({ name: 'PaginationStub' }).at(-1)!
    pagination.vm.$emit('update:page', 1); await flushPromises()
    const oldSignal = api.inventory.mock.calls[1][1] as AbortSignal
    pagination.vm.$emit('update:page', 2); await flushPromises()
    expect(oldSignal.aborted).toBe(true)
    finishOld({ ...page([catalogItem(91, 'obsolete-catalog')]), total: 60 }); await flushPromises()
    expect(wrapper.text()).toContain('newer-catalog')
    expect(wrapper.text()).not.toContain('obsolete-catalog')
    expect(pagination.props('page')).toBe(2)
  })

  it('catalog race: invalidates pending pages on page-size changes and closing the dialog', async () => {
    const catalogItem = (id: number, name: string) => ({ ...item, id, account_name: name, upstream_model: '', result: { status: 'available', models: [] } })
    api.inventory.mockResolvedValueOnce({ ...page([catalogItem(90, 'first-catalog')]), total: 60 })
    const wrapper = renderView(); await flushPromises(); await wrapper.get('[data-test="capability-catalog"]').trigger('click'); await flushPromises()
    const pending: Array<(value: unknown) => void> = []
    api.inventory.mockImplementation(() => new Promise((resolve) => { pending.push(resolve) }))
    const pagination = wrapper.findAllComponents({ name: 'PaginationStub' }).at(-1)!
    pagination.vm.$emit('update:page', 2); await flushPromises()
    const pageSignal = api.inventory.mock.calls[1][1] as AbortSignal
    pagination.vm.$emit('update:pageSize', 50); await flushPromises()
    expect(pageSignal.aborted).toBe(true)
    const sizeSignal = api.inventory.mock.calls[2][1] as AbortSignal
    wrapper.findAllComponents({ name: 'CapabilityDialogStub' }).find((dialog) => dialog.props('title') === 'admin.accountCapabilities.catalog')!.vm.$emit('close')
    await flushPromises(); expect(sizeSignal.aborted).toBe(true)
    api.inventory.mockResolvedValueOnce({ ...page([catalogItem(93, 'reopened-catalog')]), total: 60, page_size: 50 })
    await wrapper.get('[data-test="capability-catalog"]').trigger('click'); await flushPromises()
    for (const resolve of pending) resolve({ ...page([catalogItem(91, 'obsolete-catalog')]), total: 60 })
    await flushPromises()
    expect(wrapper.text()).toContain('reopened-catalog')
    expect(wrapper.text()).not.toContain('obsolete-catalog')
  })

  it('publication alternatives: respects server eligibility even when an old probe says alive', async () => {
    api.candidates.mockResolvedValue({ ...page([{ ...candidate, publishable: false, not_publishable_reasons: ['evidence_older_than_24h'] }]), accounts: scopeAccounts })
    const wrapper = renderView(); await flushPromises(); await selectCandidates(wrapper)
    expect(wrapper.get('[data-test="capability-prepare-preview"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('evidence_older_than_24h')
    expect(api.preview).not.toHaveBeenCalled()
  })

  it('publication alternatives: retains every eligible target and removing a draft row remains a merge', async () => {
    const vip = { ...candidate, candidate_id: 'vip-http', upstream_model: 'gpt-6-astra-vip', tier: 'vip' as const, publishable: true, latest_probe_item_id: 41 }
    const vipWS = { ...vip, candidate_id: 'vip-ws', protocol: 'responses_websocket' as const, latest_probe_item_id: 42 }
    const ssvip = { ...candidate, candidate_id: 'ssvip-http', upstream_model: 'gpt-6-astra-ssvip', publishable: true, latest_probe_item_id: 43 }
    api.candidates.mockResolvedValue({ ...page([vip, vipWS, ssvip]), accounts: scopeAccounts })
    const wrapper = renderView(); await flushPromises(); await selectCandidates(wrapper, ['vip-http', 'vip-ws', 'ssvip-http'])
    await wrapper.get('[data-test="capability-prepare-preview"]').trigger('click'); await flushPromises()
    expect(wrapper.find('[data-test="capability-draft-alternatives"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-test^="capability-remove-draft-"]')).toHaveLength(3)
    await wrapper.get('[data-test="capability-generate-preview"]').trigger('click'); await flushPromises()
    expect(api.preview.mock.calls[0][0].groups[0].models[0].evidence_ids).toEqual([41, 42, 43])
    await wrapper.get('[data-test="capability-remove-draft-0"]').trigger('click'); await flushPromises()
    expect(wrapper.find('[data-test="capability-apply"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-test^="capability-remove-draft-"]')).toHaveLength(2)
    await wrapper.get('[data-test="capability-generate-preview"]').trigger('click'); await flushPromises()
    expect(api.preview.mock.calls[1][0].operation).toBe('merge')
    expect(api.preview.mock.calls[1][0].groups[0].models[0].evidence_ids).toEqual([42, 43])
  })
})
