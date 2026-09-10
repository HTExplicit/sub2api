import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import type {
  CapabilityCandidate, CapabilityChangeset, CapabilityModelOverview, CapabilityOverview,
  CapabilityPlan, CapabilityRun, CreateCapabilityRun
} from '@/api/admin/accountCapabilities'
import PublicModelManager from '../components/PublicModelManager.vue'

const api = vi.hoisted(() => ({
  overview: vi.fn(), plan: vi.fn(), createRun: vi.fn(), getRun: vi.fn(),
  getRunReceipt: vi.fn(), getChangeset: vi.fn(), controlRun: vi.fn(), preview: vi.fn(), apply: vi.fn()
}))

vi.mock('@/api/admin/accountCapabilities', () => ({ default: api }))

// A deterministic, interpolating translator keeps assertions about business
// states independent from copy editing without turning unknown values into truth.
const messages: Record<string, string> = {
  'common.close': '关闭',
  'admin.accountCapabilities.states.alive': '判活通过',
  'admin.accountCapabilities.states.temporary_failure': '暂时不可用',
  'admin.accountCapabilities.states.completed': '已完成',
  'admin.accountCapabilities.manager.isPublished': '已经对外开放',
  'admin.accountCapabilities.manager.notPublished': '尚未对外开放',
  'admin.accountCapabilities.manager.accountCount': '{count} 个账号',
  'admin.accountCapabilities.manager.modelEvidence': '判活通过 {passed} 个账号，未测 {untested} 个账号',
  'admin.accountCapabilities.manager.basicPassed': '基础判活通过',
  'admin.accountCapabilities.manager.notCheckedYet': '尚未测试',
  'admin.accountCapabilities.manager.cannotReceiveNow': '目前不可接单',
  'admin.accountCapabilities.manager.canReceiveNow': '目前可接单',
  'admin.accountCapabilities.manager.waitingPrice': '判活通过，等待定价',
  'admin.accountCapabilities.manager.nameNeedsReview': '待确认名称',
  'admin.accountCapabilities.manager.reviewName': '查看名称依据',
  'admin.accountCapabilities.manager.viewAccounts': '查看账号',
  'admin.accountCapabilities.manager.setPrice': '设置价格',
  'admin.accountCapabilities.manager.checkUntested': '检查未测部分',
  'admin.accountCapabilities.manager.reviewRecommendation': '确认推荐变化',
  'admin.accountCapabilities.manager.lastSuccess': '最近成功：{time}',
  'admin.accountCapabilities.manager.latestAttempt': '最近尝试：{status}，{time}',
  'admin.accountCapabilities.manager.startFailed': '发送状态不确定，请重试同一操作',
  'admin.accountCapabilities.manager.scopeUnavailable': '当前来源或分组已不可用，请调整筛选后重试',
  'admin.accountCapabilities.manager.addingRoutes': '补充 {count} 条备用线路',
  'admin.accountCapabilities.manager.unexpectedRemoval': '发现未明确要求的删除，已阻止应用'
}

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, values: Record<string, string | number> = {}) => (messages[key] ?? key)
      .replace(/\{(\w+)\}/g, (_, name: string) => String(values[name] ?? `{${name}}`)),
    te: (key: string) => Object.hasOwn(messages, key)
  })
}))

const groupID = 50
const groupName = 'claude(非逆向渠道)'
const fable5 = 'claude-fable-5'
const fable51 = 'claude-fable-5.1'
const retainedModel = 'claude-sonnet-4.6'
const scope = { folder_ids: [7, 8], account_ids: [101, 102, 103] }

function candidate(overrides: Partial<CapabilityCandidate> = {}): CapabilityCandidate {
  return {
    candidate_id: 'account-101-fable51-messages', account_id: 101, account_name: 'dmxapi 主线',
    folder_id: 7, public_model: fable51, upstream_model: fable51, aliases: [], protocol: 'messages',
    profile: 'text', tier: 'standard', group_id: groupID, group_name: groupName,
    discovered: true, configured: false, published: false, discovery_status: 'discovered',
    stale: false, warnings: [], last_success_item_id: 1001, last_success_reusable: true,
    last_success_at: '2026-08-01T00:00:00Z', routing_ready: true, ...overrides
  }
}

function model(publicModel: string, overrides: Partial<CapabilityModelOverview> = {}): CapabilityModelOverview {
  return {
    public_model: publicModel, aliases: [], tier: 'standard', published: false,
    verified_account_count: 0, routing_ready_account_count: 0, untested_account_count: 0,
    temporary_failure_account_count: 0, pricing_known: true, needs_name_confirmation: false,
    last_checked_at: null, last_check_status: 'untested', action: 'view', reasons: [], candidates: [], ...overrides
  }
}

function overview(): CapabilityOverview {
  const candidates = [
    candidate(),
    candidate({ candidate_id: 'account-101-fable51-alias', upstream_model: 'vendor/claude-fable-5.1-VIP', aliases: [fable51], protocol: 'responses' }),
    candidate({
      candidate_id: 'account-102-fable51', account_id: 102, account_name: '白嫖备用', folder_id: 8,
      protocol: 'chat_completions', routing_ready: false, last_success_item_id: 1002,
      latest_attempt: { item_id: 1050, status: 'temporary_failure', classification: 'timeout', stale: false, account_failure: false, checked_at: '2026-09-09T00:00:00Z' }
    })
  ]
  return {
    scope: structuredClone(scope),
    accounts: [
      { id: 101, name: 'dmxapi 主线', folder_id: 7, platform: 'anthropic', status: 'active', schedulable: true },
      { id: 102, name: '白嫖备用', folder_id: 8, platform: 'openai', status: 'active', schedulable: true },
      { id: 103, name: '未测账号', folder_id: 8, platform: 'anthropic', status: 'active', schedulable: true }
    ],
    groups: [{
      id: groupID, name: groupName, platform: 'composite', rate_multiplier: 0.5,
      published_model_count: 1, verified_account_count: 2, routing_ready_account_count: 1, attention_count: 4,
      models: [
        model(fable5, { action: 'check_untested', untested_account_count: 1 }),
        model(fable51, { action: 'add', verified_account_count: 2, routing_ready_account_count: 1, last_check_status: 'alive', candidates }),
        model(retainedModel, { published: true, verified_account_count: 1, last_check_status: 'temporary_failure', temporary_failure_account_count: 1 }),
        model('vendor-mystery-SSVIP', { action: 'review_name', needs_name_confirmation: true }),
        model('claude-fable-5.2', { action: 'set_price', verified_account_count: 1, pricing_known: false, last_check_status: 'alive' })
      ]
    }, {
      id: 51, name: 'gemini', platform: 'openai', rate_multiplier: 0.2,
      published_model_count: 1, verified_account_count: 1, routing_ready_account_count: 1, attention_count: 0,
      models: [model('gemini-3-pro', { published: true, verified_account_count: 1, routing_ready_account_count: 1, last_check_status: 'alive' })]
    }],
    // Account 101 is shared between two groups; group counts must not be summed.
    totals: { group_count: 2, published_model_count: 2, verified_account_count: 2, routing_ready_account_count: 1, attention_count: 4 }
  }
}

const probeRequest: CreateCapabilityRun = {
  kind: 'probe', folder_ids: [8], account_ids: [103], only_untested: true,
  expected_config_fingerprints: { '103': 'exact-config-fingerprint' },
  items: [{ account_id: 103, upstream_model: 'namespace/claude-fable-5-SSVIP', protocol: 'messages', profile: 'text', aliases: [fable5] }]
}

function plan(includeUntested = true): CapabilityPlan {
  return {
    scope: structuredClone(scope), probe_request: includeUntested ? structuredClone(probeRequest) : null,
    maximum_request_count: includeUntested ? 1 : 0,
    preview_request: {
      operation: 'merge', scope: structuredClone(scope), groups: [{
        id: groupID, name: groupName, platform: 'composite', rate_multiplier: 0.5,
        models: [{ public_model: fable51, evidence_ids: [1001, 1002] }]
      }]
    },
    impact: {
      added_models: [{ group_id: groupID, group_name: groupName, public_model: fable51 }],
      added_accounts: [
        { group_id: groupID, group_name: groupName, public_model: fable51, account_id: 101, account_name: 'dmxapi 主线' },
        { group_id: groupID, group_name: groupName, public_model: fable51, account_id: 102, account_name: '白嫖备用' }
      ],
      retained_models: [{ group_id: groupID, group_name: groupName, public_model: retainedModel }],
      removed_models: [], removed_accounts: [], reused_success_count: 2
    },
    exclusions: [], warnings: []
  }
}

function changeset(status: CapabilityChangeset['status'] = 'preview'): CapabilityChangeset {
  return {
    id: 90, scope: structuredClone(scope), status, created_at: '2026-09-09T00:00:00Z', warnings: [],
    changes: [{ kind: 'allowlist', group_id: groupID, label: '补充 Fable 5.1', before: [retainedModel], after: [retainedModel, fable51], evidence_ids: [1001, 1002] }]
  }
}

function run(status: CapabilityRun['status'] = 'running'): CapabilityRun {
  return {
    id: 80, created_by: 1, kind: 'probe', folder_ids: [8], account_ids: [103], status,
    target_count: 1, processed_count: status === 'completed' ? 1 : 0,
    succeeded_count: status === 'completed' ? 1 : 0, failed_count: 0, request_count: 1,
    created_at: '2026-09-09T00:00:00Z', updated_at: '2026-09-09T00:00:00Z'
  }
}

const wrappers: VueWrapper[] = []

function unmountManager(wrapper: VueWrapper) {
  const index = wrappers.indexOf(wrapper)
  if (index >= 0) wrappers.splice(index, 1)
  wrapper.unmount()
}

function cachedSessionValues(): string {
  return Array.from({ length: sessionStorage.length }, (_, index) => sessionStorage.getItem(sessionStorage.key(index)!) ?? '').join('\n')
}

function expectNoResumeMutations() {
  for (const action of [api.plan, api.createRun, api.controlRun, api.preview, api.apply]) expect(action).not.toHaveBeenCalled()
}

async function mountManager(props: Record<string, unknown> = {}) {
  const router = createRouter({
    history: createMemoryHistory(), routes: [
      { path: '/admin/account-capabilities', component: { template: '<div />' } },
      { path: '/admin/channels/pricing', component: { template: '<div />' } },
      { path: '/admin/accounts', component: { template: '<div />' } }
    ]
  })
  await router.push('/admin/account-capabilities')
  await router.isReady()
  const wrapper = mount(PublicModelManager, {
    props: {
      folderIds: [7, 8], accountIds: [], groupIds: [],
      folders: [
        { id: 7, name: 'dmxapi', sort_order: 0, account_count: 1, created_at: '2026-08-01T00:00:00Z', updated_at: '2026-08-01T00:00:00Z' },
        { id: 8, name: '白嫖', sort_order: 1, account_count: 2, created_at: '2026-08-01T00:00:00Z', updated_at: '2026-08-01T00:00:00Z' }
      ], ...props
    },
    global: {
      plugins: [router],
      stubs: { BaseDialog: { props: ['show', 'title'], template: '<section v-if="show" role="dialog" :aria-label="title"><slot /><footer><slot name="footer" /></footer></section>' } }
    }
  })
  wrappers.push(wrapper)
  await flushPromises()
  return wrapper
}

function row(wrapper: VueWrapper, publicModel: string) {
  return wrapper.get(`[data-test="public-model-row"][data-model="${publicModel}"]`)
}

async function openGroup(wrapper: VueWrapper) {
  await wrapper.get(`[data-group="${groupName}"]`).trigger('click')
}

describe('PublicModelManager beginner flow', () => {
  beforeEach(() => {
    sessionStorage.clear()
    Object.values(api).forEach((mock) => mock.mockReset())
    api.overview.mockImplementation(async () => overview())
    api.plan.mockImplementation(async () => plan())
    api.createRun.mockImplementation(async () => run())
    api.getRun.mockImplementation(async () => run())
    api.getRunReceipt.mockImplementation(async () => run())
    api.getChangeset.mockImplementation(async () => changeset())
    api.preview.mockImplementation(async () => changeset())
    api.apply.mockImplementation(async () => changeset('applied'))
  })

  afterEach(() => {
    wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
    vi.useRealTimers()
  })

  it('starts with a read-only group overview, keeps Fable versions distinct, and never recomputes server totals from search', async () => {
    const wrapper = await mountManager()
    expect(api.overview).toHaveBeenCalledTimes(1)
    expect(api.overview).toHaveBeenCalledWith({ folder_ids: '7,8', account_ids: undefined, group_ids: undefined }, expect.any(AbortSignal))
    expect(wrapper.get('[data-test="manager-group-grid"]').findAll('button')).toHaveLength(2)
    expect(wrapper.findAll('[data-test="public-model-row"]')).toHaveLength(0)
    expect(wrapper.text()).toContain('dmxapi、白嫖')
    expect(wrapper.get('[data-test="manager-verified-count"]').text()).toBe('2')

    await openGroup(wrapper)
    expect(row(wrapper, fable5).attributes('data-model')).not.toBe(row(wrapper, fable51).attributes('data-model'))
    expect(row(wrapper, fable5).text()).toContain('尚未测试')
    await wrapper.get('[data-test="manager-model-search"]').setValue(fable51)
    expect(wrapper.findAll('[data-test="public-model-row"]')).toHaveLength(1)
    expect(wrapper.get('[data-test="manager-published-count"]').text()).toBe('2')
    expect(wrapper.get('[data-test="manager-verified-count"]').text()).toBe('2')
    for (const action of [api.plan, api.createRun, api.preview, api.apply]) expect(action).not.toHaveBeenCalled()
  })

  it('separates successful checks from publication and keeps temporarily unavailable published models visibly published', async () => {
    const wrapper = await mountManager()
    await openGroup(wrapper)
    const unpublishedSuccess = row(wrapper, fable51)
    expect(unpublishedSuccess.text()).toContain('尚未对外开放')
    expect(unpublishedSuccess.text()).toContain('判活通过 2 个账号')
    expect(unpublishedSuccess.get('dl').text()).toContain('判活通过')
    const cooldown = row(wrapper, retainedModel)
    expect(cooldown.text()).toContain('已经对外开放')
    expect(cooldown.text()).toContain('0 个账号')
    expect(cooldown.text()).toContain('暂时不可用')
    expect(cooldown.text()).not.toContain('尚未对外开放')
    expect(api.createRun).not.toHaveBeenCalled()
  })

  it('shows unknown names for review and links successful unpriced models to pricing without calling them unsupported', async () => {
    const wrapper = await mountManager()
    await openGroup(wrapper)
    const unknown = row(wrapper, 'vendor-mystery-SSVIP')
    expect(unknown.text()).toContain('待确认名称')
    expect(unknown.findAll('button')).toHaveLength(1)
    expect(unknown.get('button').text()).toBe('查看名称依据')
    const unpriced = row(wrapper, 'claude-fable-5.2')
    expect(unpriced.text()).toContain('判活通过，等待定价')
    expect(unpriced.get('a').attributes('href')).toBe('/admin/channels/pricing')
    expect(unpriced.text()).not.toMatch(/不支持|不可用/)
    expect(api.createRun).not.toHaveBeenCalled()
  })

  it('freezes the chosen group or exact model scope when asking the server for a recommendation', async () => {
    const wrapper = await mountManager()
    await openGroup(wrapper)
    await wrapper.get('[data-test="manager-organize"]').trigger('click')
    await flushPromises()
    expect(api.plan).toHaveBeenLastCalledWith({ scope, mainstream_only: true, group_ids: [groupID] }, expect.any(AbortSignal))
    await wrapper.get('[role="dialog"] footer button').trigger('click')
    await row(wrapper, fable5).get('button').trigger('click')
    await flushPromises()
    expect(api.plan).toHaveBeenLastCalledWith({ scope, mainstream_only: true, models: [{ group_id: groupID, group_name: groupName, public_model: fable5 }] }, expect.any(AbortSignal))
    expect(api.createRun).not.toHaveBeenCalled()
    expect(api.apply).not.toHaveBeenCalled()
  })

  it('sends the exact untested probe request only on explicit confirmation and reuses its idempotency key after an uncertain response', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    api.createRun.mockRejectedValueOnce(new Error('Network receipt unknown')).mockResolvedValueOnce(run())
    const wrapper = await mountManager()
    await wrapper.get('[data-test="manager-organize"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="manager-probe-plan"]').exists()).toBe(true)
    expect(api.createRun).not.toHaveBeenCalled()

    await wrapper.get('[data-test="manager-start-checks"]').trigger('click')
    await flushPromises()
    const [firstRequest, firstKey] = api.createRun.mock.calls[0]!
    expect(firstRequest).toEqual(probeRequest)
    expect(firstKey).toMatch(/^account_capability_/)
    expect(wrapper.text()).toContain('发送状态不确定')
    await vi.advanceTimersByTimeAsync(16000)
    expect(api.createRun).toHaveBeenCalledTimes(1)
    expect(api.getRun).not.toHaveBeenCalled()
    expect(api.preview).not.toHaveBeenCalled()

    await wrapper.get('[data-test="manager-start-checks"]').trigger('click')
    await flushPromises()
    expect(api.createRun).toHaveBeenNthCalledWith(2, probeRequest, firstKey)
    expect(api.plan).toHaveBeenCalledTimes(1)
    expect(api.apply).not.toHaveBeenCalled()
  })

  it('continues completed checks directly into a merge preview, preserves unselected models, and applies exactly once only after confirmation', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    api.plan.mockResolvedValueOnce(plan()).mockResolvedValueOnce(plan(false))
    api.getRun.mockResolvedValue(run('completed'))
    const wrapper = await mountManager()
    await wrapper.get('[data-test="manager-organize"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="manager-start-checks"]').trigger('click')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(4000)
    await flushPromises()

    expect(api.plan).toHaveBeenCalledTimes(2)
    expect(api.preview).toHaveBeenCalledTimes(1)
    expect(api.preview).toHaveBeenCalledWith({ ...plan(false).preview_request, operation: 'merge', idempotency_key: expect.any(String) })
    expect(api.preview.mock.calls[0]![0].groups[0].models.map((item: { public_model: string }) => item.public_model)).not.toContain(retainedModel)
    expect(wrapper.get('[role="dialog"]').text()).toContain(retainedModel)
    expect(wrapper.find('[data-test="manager-checks-next"]').exists()).toBe(false)
    expect(api.apply).not.toHaveBeenCalled()

    let resolveApply!: (value: CapabilityChangeset) => void
    api.apply.mockImplementationOnce(() => new Promise<CapabilityChangeset>((resolve) => { resolveApply = resolve }))
    const confirm = wrapper.get('[data-test="manager-confirm-apply"]')
    await Promise.all([confirm.trigger('click'), confirm.trigger('click')])
    expect(api.apply).toHaveBeenCalledTimes(1)
    expect(api.apply).toHaveBeenCalledWith(90)
    resolveApply(changeset('applied'))
    await flushPromises()
    expect(wrapper.find('[data-test="manager-applied"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="manager-confirm-apply"]').exists()).toBe(false)
    expect(api.createRun).toHaveBeenCalledTimes(1)
  })

  it('deduplicates aliases in account details while keeping historical success and the latest failed attempt separate', async () => {
    const wrapper = await mountManager()
    await openGroup(wrapper)
    const accountsButton = row(wrapper, fable51).findAll('button').find((button) => button.text() === '查看账号')!
    await accountsButton.trigger('click')
    const accountRows = wrapper.findAll('[data-test="manager-account-detail"]')
    expect(accountRows).toHaveLength(2)
    expect(accountRows.map((item) => item.get('h3').text()).sort()).toEqual(['dmxapi 主线', '白嫖备用'].sort())
    expect(accountRows.every((item) => item.text().includes('基础判活通过'))).toBe(true)
    const fallback = accountRows.find((item) => item.get('h3').text() === '白嫖备用')!
    expect(fallback.text()).toContain('最近成功：')
    expect(fallback.text()).toContain('最近尝试：暂时不可用')
    expect(fallback.text()).toContain('目前不可接单')
    expect(api.createRun).not.toHaveBeenCalled()
  })

  it('blocks application when a recommendation would remove an unselected existing model', async () => {
    const unsafe = plan(false)
    unsafe.impact.removed_models = [{ group_id: groupID, group_name: groupName, public_model: retainedModel }]
    api.plan.mockResolvedValue(unsafe)
    const wrapper = await mountManager()
    await wrapper.get('[data-test="manager-organize"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="manager-review-existing"]').trigger('click')
    await flushPromises()
    const confirm = wrapper.get('[data-test="manager-confirm-apply"]')
    expect(confirm.attributes('disabled')).toBeDefined()
    expect(wrapper.get('[role="dialog"]').text()).toContain('发现未明确要求的删除，已阻止应用')
    await confirm.trigger('click')
    expect(api.apply).not.toHaveBeenCalled()
    expect(api.createRun).not.toHaveBeenCalled()
  })

  it('contract follow-up: forwards the server configuration-bound preview idempotency key unchanged', async () => {
    const saved = plan(false)
    saved.preview_request!.idempotency_key = 'server-plan-current-config-6db824c1'
    saved.preview_request!.expected_config_revisions = { accounts: { '101': 'account-current-config' }, groups: { '50': 'group-current-config' } }
    api.plan.mockResolvedValue(saved)
    const wrapper = await mountManager()
    await wrapper.get('[data-test="manager-organize"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="manager-review-existing"]').trigger('click')
    await flushPromises()

    expect(api.preview).toHaveBeenCalledTimes(1)
    expect(api.preview).toHaveBeenCalledWith(saved.preview_request)
    expect(api.preview.mock.calls[0]![0].idempotency_key).toBe('server-plan-current-config-6db824c1')
    expect(api.createRun).not.toHaveBeenCalled()
    expect(api.apply).not.toHaveBeenCalled()
  })

  it('contract follow-up: explains new branches on an existing account even when no account or model is added', async () => {
    const saved = plan(false)
    saved.impact.added_models = []
    saved.impact.added_accounts = []
    saved.impact.retained_models.push({ group_id: groupID, group_name: groupName, public_model: fable51 })
    saved.impact.added_routes = [
      { group_id: groupID, group_name: groupName, public_model: fable51, account_id: 101, account_name: 'dmxapi 主线', upstream_model: 'vendor/claude-fable-5.1-VIP', protocol: 'messages' },
      { group_id: groupID, group_name: groupName, public_model: fable51, account_id: 101, account_name: 'dmxapi 主线', upstream_model: 'vendor/claude-fable-5.1-SSVIP', protocol: 'responses' }
    ]
    api.plan.mockResolvedValue(saved)
    const branchChangeset = changeset()
    branchChangeset.changes = [{ kind: 'routes', group_id: groupID, label: '补充已判活备用线路', before: { branch_count: 1 }, after: { branch_count: 3 }, evidence_ids: [1001, 1002] }]
    api.preview.mockResolvedValue(branchChangeset)
    const wrapper = await mountManager()
    await wrapper.get('[data-test="manager-organize"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="manager-review-existing"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-test="manager-impact"]').findAll('strong').map((stat) => stat.text()).slice(0, 2)).toEqual(['0', '0'])
    const routes = wrapper.get('[data-test="manager-added-routes"]')
    expect(routes.text()).toContain('补充 2 条备用线路')
    expect(routes.text()).toContain(fable51)
    expect(routes.text()).toContain(groupName)
    expect(routes.text().match(/dmxapi 主线/g)).toHaveLength(1)
    expect(wrapper.get('[data-test="manager-confirm-apply"]').attributes('disabled')).toBeUndefined()
    expect(api.apply).not.toHaveBeenCalled()
    expect(api.createRun).not.toHaveBeenCalled()
  })

  it('contract follow-up: explains a rejected source scope in plain language without starting any mutations', async () => {
    api.overview.mockRejectedValue({ response: { status: 400, data: { message: 'RAW_UPSTREAM_DETAILS_MUST_NOT_RENDER' } } })
    const wrapper = await mountManager({ folderIds: [], accountIds: [999], groupIds: [groupID] })

    expect(api.overview).toHaveBeenCalledWith({ folder_ids: undefined, account_ids: '999', group_ids: String(groupID) }, expect.any(AbortSignal))
    expect(wrapper.get('[role="alert"]').text()).toContain('当前来源或分组已不可用，请调整筛选后重试')
    expect(wrapper.text()).not.toContain('RAW_UPSTREAM_DETAILS_MUST_NOT_RENDER')
    expect(wrapper.get('[data-test="manager-organize"]').attributes('disabled')).toBeDefined()
    for (const action of [api.plan, api.createRun, api.controlRun, api.preview, api.apply]) expect(action).not.toHaveBeenCalled()
  })

  it('resume: restores running work and an uncertain receipt by GET without replaying or replacing the original request', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    const first = await mountManager()
    await first.get('[data-test="manager-organize"]').trigger('click')
    await flushPromises()
    await first.get('[data-test="manager-start-checks"]').trigger('click')
    await flushPromises()
    expect(api.createRun).toHaveBeenCalledTimes(1)
    expect(sessionStorage.length).toBeGreaterThan(0)
    expect(cachedSessionValues()).not.toContain(probeRequest.items![0]!.upstream_model)
    expect(cachedSessionValues()).not.toContain('exact-config-fingerprint')
    unmountManager(first)
    Object.values(api).forEach((mock) => mock.mockClear())

    const running = await mountManager()
    expect(api.getRun).toHaveBeenCalledWith(80)
    expect(running.find('[data-test="manager-resume"]').exists()).toBe(true)
    expectNoResumeMutations()
    unmountManager(running)

    sessionStorage.clear()
    Object.values(api).forEach((mock) => mock.mockClear())
    api.createRun.mockRejectedValueOnce(new Error('Connection closed before receipt'))
    const unknown = await mountManager()
    await unknown.get('[data-test="manager-organize"]').trigger('click')
    await flushPromises()
    await unknown.get('[data-test="manager-start-checks"]').trigger('click')
    await flushPromises()
    expect(api.createRun).toHaveBeenCalledTimes(1)
    const originalKey = api.createRun.mock.calls[0]![1]
    expect(cachedSessionValues()).toContain(originalKey)
    expect(cachedSessionValues()).not.toContain(probeRequest.items![0]!.upstream_model)
    expect(cachedSessionValues()).not.toContain('exact-config-fingerprint')
    unmountManager(unknown)
    Object.values(api).forEach((mock) => mock.mockClear())
    api.getRunReceipt.mockRejectedValue({ response: { status: 404 } })

    const unresolved = await mountManager()
    expect(api.getRunReceipt).toHaveBeenCalledWith(originalKey)
    expect(unresolved.find('[data-test="manager-resume"]').exists()).toBe(true)
    expectNoResumeMutations()
    await unresolved.get('[data-test="manager-resume"]').trigger('click')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(8000)
    expectNoResumeMutations()
    expect(api.getRunReceipt.mock.calls.every(([key]) => key === originalKey)).toBe(true)
    expect(cachedSessionValues()).toContain(originalKey)
  })

  it('resume: waits for explicit continuation before planning completed work and restores a saved preview without a new run', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    const first = await mountManager()
    await first.get('[data-test="manager-organize"]').trigger('click')
    await flushPromises()
    await first.get('[data-test="manager-start-checks"]').trigger('click')
    await flushPromises()
    unmountManager(first)
    Object.values(api).forEach((mock) => mock.mockClear())
    api.getRun.mockResolvedValue(run('completed'))
    api.plan.mockResolvedValue(plan(false))

    const completed = await mountManager()
    expect(api.getRun).toHaveBeenCalledWith(80)
    expectNoResumeMutations()
    await completed.get('[data-test="manager-resume"]').trigger('click')
    await flushPromises()
    expect(api.plan).toHaveBeenCalledTimes(1)
    expect(api.preview).toHaveBeenCalledTimes(1)
    expect(api.createRun).not.toHaveBeenCalled()
    expect(api.controlRun).not.toHaveBeenCalled()
    expect(api.apply).not.toHaveBeenCalled()
    expect(completed.find('[data-test="manager-confirm-apply"]').exists()).toBe(true)
    unmountManager(completed)
    Object.values(api).forEach((mock) => mock.mockClear())

    const preview = await mountManager()
    expect(api.getChangeset).toHaveBeenCalledWith(90)
    expectNoResumeMutations()
    await preview.get('[data-test="manager-resume"]').trigger('click')
    await flushPromises()
    expectNoResumeMutations()
    expect(preview.find('[data-test="manager-confirm-apply"]').exists()).toBe(true)
  })

  it('resume: keeps frozen source scopes isolated and restores work only for the matching entry filters', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    const original = await mountManager()
    await original.get('[data-test="manager-organize"]').trigger('click')
    await flushPromises()
    await original.get('[data-test="manager-start-checks"]').trigger('click')
    await flushPromises()
    Object.values(api).forEach((mock) => mock.mockClear())
    const restricted = overview()
    restricted.scope = { folder_ids: [8], account_ids: [103] }
    restricted.accounts = restricted.accounts.filter((account) => account.id === 103)
    restricted.groups = restricted.groups.filter((group) => group.id === groupID)
    api.overview.mockResolvedValue(restricted)

    await original.setProps({ folderIds: [8], accountIds: [103], groupIds: [groupID] })
    await flushPromises()
    expect(original.find('[data-test="manager-resume"]').exists()).toBe(false)
    expect(api.getRun).not.toHaveBeenCalled()
    expect(api.getRunReceipt).not.toHaveBeenCalled()
    expectNoResumeMutations()
    unmountManager(original)

    const different = await mountManager({ folderIds: [8], accountIds: [103], groupIds: [groupID] })
    expect(different.find('[data-test="manager-resume"]').exists()).toBe(false)
    expect(api.getRun).not.toHaveBeenCalled()
    expectNoResumeMutations()
    unmountManager(different)
    api.overview.mockResolvedValue(overview())

    const matching = await mountManager()
    expect(api.getRun).toHaveBeenCalledWith(80)
    expect(matching.find('[data-test="manager-resume"]').exists()).toBe(true)
    expectNoResumeMutations()
  })

  it('recovery follow-up: respects the model search when freezing the recommendation and never expands an empty match', async () => {
    const wrapper = await mountManager({ initialSearch: fable51 })
    await wrapper.get('[data-test="manager-organize"]').trigger('click')
    await flushPromises()
    expect(api.plan).toHaveBeenCalledWith({ scope, mainstream_only: true, models: [{ group_id: groupID, group_name: groupName, public_model: fable51 }] }, expect.any(AbortSignal))
    await wrapper.get('[role="dialog"] footer button').trigger('click')
    await wrapper.get('[data-test="manager-model-search"]').setValue('does-not-exist')
    expect(wrapper.get('[data-test="manager-organize"]').attributes('disabled')).toBeDefined()
    expect(api.plan).toHaveBeenCalledTimes(1)
    expect(api.createRun).not.toHaveBeenCalled()
  })

  it('recovery follow-up: explains normalized API scope failures without exposing provider messages', async () => {
    api.overview.mockRejectedValue({ status: 400, code: 'ACCOUNT_CAPABILITY_INVALID', message: 'RAW_UPSTREAM_DETAILS_MUST_NOT_RENDER' })
    const wrapper = await mountManager()
    expect(wrapper.get('[role="alert"]').text()).toContain('当前来源或分组已不可用')
    expect(wrapper.text()).not.toContain('RAW_UPSTREAM_DETAILS_MUST_NOT_RENDER')
    expectNoResumeMutations()
  })

  it('recovery follow-up: explicitly regenerates a conflicting recommendation without another check or automatic apply', async () => {
    api.plan.mockResolvedValue(plan(false))
    api.apply.mockRejectedValueOnce({ status: 409, code: 'ACCOUNT_CAPABILITY_SCOPE_CHANGED' })
    const wrapper = await mountManager()
    await wrapper.get('[data-test="manager-organize"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="manager-review-existing"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="manager-confirm-apply"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="manager-confirm-apply"]').attributes('disabled')).toBeDefined()
    expect(api.plan).toHaveBeenCalledTimes(1)
    await wrapper.get('[data-test="manager-regenerate"]').trigger('click')
    await flushPromises()
    expect(api.plan).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-test="manager-confirm-apply"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="manager-review-existing"]').attributes('disabled')).toBeUndefined()
    expect(api.createRun).not.toHaveBeenCalled()
    expect(api.apply).toHaveBeenCalledTimes(1)
    expect(api.preview).toHaveBeenCalledTimes(1)
  })

  it('recovery follow-up: releases a definitively rejected probe handle and offers a fresh read-only plan', async () => {
    api.createRun.mockRejectedValueOnce({ status: 409, code: 'ACCOUNT_CAPABILITY_ALREADY_ATTEMPTED' })
    const wrapper = await mountManager()
    await wrapper.get('[data-test="manager-organize"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="manager-start-checks"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="manager-start-checks"]').attributes('disabled')).toBeDefined()
    expect(sessionStorage.length).toBe(0)
    await wrapper.get('[data-test="manager-regenerate"]').trigger('click')
    await flushPromises()
    expect(api.plan).toHaveBeenCalledTimes(2)
    expect(api.createRun).toHaveBeenCalledTimes(1)
    expect(api.getRunReceipt).not.toHaveBeenCalled()
    expect(api.preview).not.toHaveBeenCalled()
    expect(api.apply).not.toHaveBeenCalled()
  })
})
