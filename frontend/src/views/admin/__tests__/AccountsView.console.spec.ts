vi.mock('@/components/admin/account-jobs/AccountOperationDialog.vue', () => ({ default: { name: 'AccountOperationDialog', props: ['job', 'show'], template: '<div v-if="show"><slot/><slot name="footer"/></div>' } }))
import { createHash } from 'node:crypto'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import AccountsView from '../AccountsView.vue'
import { adminAPI } from '@/api/admin'
import { flattenStackedColumns } from '@/components/common/columnStack'
import modelDisplayContract from '../../../../../backend/internal/service/testdata/account_available_models_contract.json'

const {
  listAccounts,
  hasAPIKeyDigest,
  getById,
  getAvailableModels,
  listWithEtag,
  getUpstreamBillingRatesWithEtag,
  getFacets,
  listFolders,
  listTags,
  getBatchTodayStats,
  getUpstreamBillingProbeSettings,
  showError,
  showSuccess,
  jobTrack,
  reviewDuplicates,
  getAllProxies,
  getAllGroups,
  updateAccount,
  accountJobsState
} = vi.hoisted(() => ({
  listAccounts: vi.fn(),
  hasAPIKeyDigest: vi.fn(),
  getById: vi.fn(),
  getAvailableModels: vi.fn(),
  listWithEtag: vi.fn(),
  getUpstreamBillingRatesWithEtag: vi.fn(),
  getFacets: vi.fn(),
  listFolders: vi.fn(),
  listTags: vi.fn(),
  getBatchTodayStats: vi.fn(),
  getUpstreamBillingProbeSettings: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  jobTrack: vi.fn(),
  reviewDuplicates: vi.fn(),
  getAllProxies: vi.fn(),
  getAllGroups: vi.fn(),
  updateAccount: vi.fn(),
  accountJobsState: { store: null as any }
}))

// AccountPriorityCell saves through the accounts API module directly.
vi.mock('@/api/admin/accounts', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/admin/accounts')>()),
  update: updateAccount
}))

vi.mock('@/stores/accountJobs', async () => {
  const { reactive } = await vi.importActual<typeof import('vue')>('vue')
  const store = reactive({ recentJobs: [] as any[], track: jobTrack, reviewDuplicates })
  accountJobsState.store = store
  return {
    isTerminalAccountJob: (job: { status?: string }) => ['succeeded', 'partially_succeeded', 'failed', 'canceled'].includes(job.status || ''),
    useAccountJobsStore: () => store
  }
})

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      list: listAccounts,
      hasAPIKeyDigest,
      getById,
      getAPIKeyVisibility: vi.fn().mockResolvedValue({ enabled: false }),
      getAvailableModels,
      listWithEtag,
      getUpstreamBillingRatesWithEtag,
      getFacets,
      listFolders,
      listTags,
      getBatchTodayStats,
      getUpstreamBillingProbeSettings,
      delete: vi.fn(),
      batchClearError: vi.fn(),
      batchRefresh: vi.fn(),
      probeUpstreamBillingBatch: vi.fn(),
      toggleSchedulable: vi.fn()
    },
    proxies: { getAll: getAllProxies },
    groups: { getAll: getAllGroups }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess,
    showInfo: vi.fn(),
    showWarning: vi.fn()
  })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ token: 'test-token', user: { id: 41, role: 'admin' }, isAuthenticated: true, isAdmin: true })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      locale: { value: 'en' },
      t: (key: string) => key
    })
  }
})

const account = {
  id: 1,
  name: 'console-account',
  platform: 'openai',
  type: 'oauth',
  status: 'active',
  schedulable: true,
  concurrency: 10,
  priority: 0,
  rate_multiplier: 1,
  credentials: {},
  extra: {},
  groups: [],
  tags: [],
  created_at: '2026-07-29T00:00:00Z',
  updated_at: '2026-07-29T00:00:00Z'
}

const ViewModeStub = {
  props: ['modelValue'],
  emits: ['update:modelValue'],
  template: `
    <div>
      <button data-test="mode-table" @click="$emit('update:modelValue', 'table')">table</button>
      <button data-test="mode-compact" @click="$emit('update:modelValue', 'compact')">compact</button>
      <button data-test="mode-cards" @click="$emit('update:modelValue', 'cards')">cards</button>
    </div>
  `
}

// the view passes stacked columns (a host carries its parts): data-columns is the flat upstream list DataTable draws
// as columns, data-hosts the stacked cells of the console theme
const DataTableStub = {
  props: ['data', 'columns'],
  emits: ['row-click'],
  methods: {
    columnClass(key: string) {
      return this.columns.find((column: { key: string }) => column.key === key)?.class || ''
    },
    flat: flattenStackedColumns
  },
  template: `
    <div data-test="view-table" :data-columns="flat(columns).map(column => column.key).join(',')" :data-hosts="columns.map(column => column.key).join(',')" :data-name-class="columnClass('name')">
      <div v-for="row in data" :key="row.id">
        <button data-test="open-row" @click="$emit('row-click', row)">{{ row.name }}</button>
        <slot name="cell-select" :row="row" />
        <slot name="cell-name" :row="row" :value="row.name" />
        <div data-test="taxonomy-cell"><slot name="cell-taxonomy_route" :row="row" /></div>
        <!-- like DataTable's row: a click that reaches it opens the row -->
        <div data-test="priority-cell" @click="$emit('row-click', row)"><slot name="cell-priority" :row="row" /></div>
      </div>
    </div>
  `
}

const DetailsDrawerStub = {
  props: ['account'],
  emits: ['edit', 'close'],
  template: '<div v-if="account" data-test="details-drawer"><span>{{ account.name }}</span><button data-test="drawer-edit" @click="$emit(\'edit\', account)">edit</button></div>'
}

const ImportDataModalStub = {
  emits: ['imported'],
  data: () => ({
    result: { id: 71, kind: 'account_import', status: 'pending' }
  }),
  template: '<button data-test="emit-import-result" @click="$emit(\'imported\', result)">imported</button>'
}

const ConsoleFiltersStub = {
  props: ['modelValue'],
  emits: ['update:modelValue', 'change'],
  template: '<div data-test="console-account-ids">{{ modelValue.account_ids.join(\',\') }}</div>'
}

const FolderBarStub = {
  props: ['folders', 'total'],
  template: '<div data-test="account-taxonomy-bar"><span data-test="folder-facet-count">{{ folders[0]?.account_count ?? -1 }}</span><span data-test="folder-navigation-total">{{ total }}</span></div>'
}

const TaxonomyManagerStub = {
  props: ['folders'],
  template: '<div data-test="taxonomy-folder-count">{{ folders[0]?.account_count ?? -1 }}</div>'
}

const AccountTestModalStub = {
  name: 'AccountTestModal',
  props: ['show', 'account'],
  emits: ['close'],
  template: '<button data-test="close-account-test" @click="$emit(\'close\')">close</button>'
}

const commonStubs = {
  AppLayout: { template: '<div><slot /></div>' },
  TablePageLayout: { template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>' },
  DataTable: DataTableStub,
  AccountCompactList: {
    props: ['accounts', 'todayStats', 'todayStatsLoading', 'todayStatsError', 'manualRefreshToken'],
    template: '<div data-test="view-compact" :data-refresh-token="String(manualRefreshToken)" :data-requests="String(todayStats[String(accounts[0]?.id)]?.requests ?? -1)">{{ accounts.length }}</div>'
  },
  AccountCardGrid: {
    props: ['accounts', 'todayStats', 'todayStatsLoading', 'todayStatsError', 'manualRefreshToken'],
    template: '<div data-test="view-cards" :data-refresh-token="String(manualRefreshToken)" :data-requests="String(todayStats[String(accounts[0]?.id)]?.requests ?? -1)">{{ accounts.length }}</div>'
  },
  AccountViewModeSwitcher: ViewModeStub,
  AccountConsoleFilters: ConsoleFiltersStub,
  AccountFolderBar: FolderBarStub,
  AccountTableActions: {
    emits: ['refresh'],
    template: '<div><button data-test="page-refresh" @click="$emit(\'refresh\')">refresh</button><slot name="beforeCreate" /><slot name="after" /></div>'
  },
  AccountBulkActionsBar: { name: 'AccountBulkActionsBar', emits: ['edit-selected'], props: ['selectedIds'], template: '<div data-test="selected-ids">{{ selectedIds.join(\',\') }}</div>' },
  AccountActionMenu: true,
  ImportDataModal: ImportDataModalStub,
  AccountDetailsDrawer: DetailsDrawerStub,
  EditAccountModal: { props: ['show', 'account'], template: '<div data-test="edit-modal" :data-show="String(show)" :data-id="account?.id || 0"></div>' },
  Pagination: true,
  ConfirmDialog: {
    name: 'ConfirmDialog',
    props: ['show', 'title', 'message'],
    emits: ['confirm', 'cancel'],
    template: '<div v-if="show" data-test="confirm-dialog"><span>{{ message }}</span><button data-test="confirm-dialog-submit" @click="$emit(\'confirm\')">confirm</button></div>'
  },
  ReAuthAccountModal: true,
  AccountTestModal: AccountTestModalStub,
  BatchAccountTestModal: true,
  AccountStatsModal: true,
  ScheduledTestsPanel: true,
  SyncFromCrsModal: true,
  TempUnschedStatusModal: true,
  ErrorPassthroughRulesModal: true,
  TLSFingerprintProfilesModal: true,
  CreateAccountModal: true,
  BulkEditAccountModal: true,
  AccountTaxonomyManager: TaxonomyManagerStub,
  PlatformTypeBadge: true,
  AccountCapacityCell: true,
  AccountStatusIndicator: true,
  AccountTodayStatsCell: true,
  AccountGroupsCell: true,
  AccountUsageCell: true,
  UpstreamBillingRateCell: true,
  HelpTooltip: true,
  Icon: true
}

const mountView = (
  plugins: any[] = [],
  props: Record<string, unknown> = {},
  slots: Record<string, any> = {},
) => mount(AccountsView, {
  props,
  slots,
  global: { stubs: commonStubs, plugins }
})

// what the search box does: the text reaches the page at once, the change once typing pauses
const typeSearch = (wrapper: ReturnType<typeof mountView>, text: string) => {
  const filters = wrapper.findComponent(ConsoleFiltersStub)
  filters.vm.$emit('update:modelValue', { ...filters.props('modelValue'), search: text })
  filters.vm.$emit('change')
}

const keyDigestTerm = (text: string) => `sha256:${createHash('sha256').update(text).digest('hex')}`

// the list reloads a debounce after the search is resolved
const SEARCH_RELOAD_WAIT = { timeout: 3000 }

// useTableLoader hands every list request the same params object, so the search of a request is copied as it is made
const recordListSearches = () => {
  const searches: unknown[] = []
  listAccounts.mockImplementation(async (_page: number, _pageSize: number, filters?: { search?: string }) => {
    searches.push(filters?.search)
    return { items: [account], total: 1, page: 1, page_size: 20, pages: 1 }
  })
  return searches
}

// every argument of every request the page made through the accounts API
const accountRequestArguments = () =>
  JSON.stringify(Object.values(adminAPI.accounts).map(request => vi.mocked(request).mock.calls))

// the refresh of the upstream billing rates the page makes after a billing probe
const refreshBillingRates = (wrapper: ReturnType<typeof mountView>): Promise<void> =>
  (wrapper.vm as any).refreshAccountsAfterUpstreamBillingProbe()

describe('admin AccountsView Cockpit console', () => {
  it('preserves the submitted bulk-edit selection in the original window after clearing page selection', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get<HTMLInputElement>('input[type="checkbox"]').setValue(true)
    wrapper.findComponent({ name: 'AccountBulkActionsBar' }).vm.$emit('edit-selected')
    await flushPromises()
    const modal = wrapper.findComponent({ name: 'BulkEditAccountModal' })
    const ids = modal.props('accountIds')
    expect(ids).toEqual([account.id])
    modal.vm.$emit('updated', { id: 91, kind: 'account_bulk_update', status: 'pending' })
    await flushPromises()
    expect(wrapper.get('[data-test="selected-ids"]').text()).toBe('')
    expect(modal.props('show')).toBe(true)
    expect(modal.props('accountIds')).toEqual(ids)
    wrapper.unmount()
  })
  beforeEach(() => {
    localStorage.clear()
    sessionStorage.clear()

    listAccounts.mockReset().mockResolvedValue({ items: [account], total: 1, page: 1, page_size: 20, pages: 1 })
    // by default a search text is not the API key of any account
    hasAPIKeyDigest.mockReset().mockResolvedValue(false)
    getById.mockReset().mockResolvedValue(account)
    getAvailableModels.mockReset().mockResolvedValue(structuredClone(modelDisplayContract.expected))
    listWithEtag.mockReset().mockResolvedValue({ notModified: true, etag: null, data: null })
    getUpstreamBillingRatesWithEtag.mockReset().mockResolvedValue({ notModified: true, etag: null, data: null })
    getFacets.mockReset().mockResolvedValue({ total: 1, uncategorized_count: 1, platforms: [], types: [], statuses: [], plans: [], proxies: [], folders: [], tags: [] })
    listFolders.mockReset().mockResolvedValue([])
    listTags.mockReset().mockResolvedValue([])
    getBatchTodayStats.mockReset().mockResolvedValue({ stats: {} })
    getUpstreamBillingProbeSettings.mockReset().mockResolvedValue({ enabled: true, interval_minutes: 30 })
    showError.mockReset()
    showSuccess.mockReset()
    jobTrack.mockReset()
    reviewDuplicates.mockReset()
    accountJobsState.store.recentJobs.splice(0)
    getAllProxies.mockReset().mockResolvedValue([])
    getAllGroups.mockReset().mockResolvedValue([])
  })

  it('uses the account model display contract for scheduled test options', async () => {
    const wrapper = mountView()
    await flushPromises()
    await (wrapper.vm as any).handleSchedule(account)
    expect(getAvailableModels).toHaveBeenCalledWith(account.id)
    expect((wrapper.vm as any).scheduleModelOptions).toEqual(modelDisplayContract.expected.map(model => ({
      value: model.id,
      label: model.display_name
    })))
    wrapper.unmount()
  })

  it('defaults to table and persists compact/card view selection', async () => {
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.find('[data-test="view-table"]').exists()).toBe(true)

    await wrapper.get('[data-test="mode-compact"]').trigger('click')
    expect(wrapper.find('[data-test="view-compact"]').exists()).toBe(true)
    expect(localStorage.getItem('account-console-view-mode')).toBe('compact')

    await wrapper.get('[data-test="mode-cards"]').trigger('click')
    expect(wrapper.find('[data-test="view-cards"]').exists()).toBe(true)
    expect(localStorage.getItem('account-console-view-mode')).toBe('cards')
    wrapper.unmount()

    const restored = mountView()
    await flushPromises()
    expect(restored.find('[data-test="view-cards"]').exists()).toBe(true)
  })

  it('shows usage in the default table order', async () => {
    const wrapper = mountView()
    await flushPromises()
    // upstream defaults: only today_stats, proxy, notes, scheduler_score and rate_multiplier are hidden;
    // the columns follow upstream v0.2.9's order (capacity, status and the scheduling switch before groups and
    // usage), with the fork's classification / route column right after the scheduling switch
    expect(wrapper.get('[data-test="view-table"]').attributes('data-columns')).toBe(
      'select,name,id,platform_type,capacity,status,schedulable,taxonomy_route,groups,usage,priority,' +
        'upstream_billing_rate,last_used_at,created_at,expires_at,actions'
    )
    // console theme: related columns are lines of one cell (名称 + 账号ID, 平台/类型 + 容量, 状态 + 调度, 管理分类/请求路由 +
    // 路由分组, 优先级 + 上游声明倍率, 最近使用 + 创建时间 + 过期时间)
    expect(wrapper.get('[data-test="view-table"]').attributes('data-hosts')).toBe(
      'select,name,platform_type,status,taxonomy_route,usage,priority,last_used_at,actions'
    )
  })

  it('shows every tag of an account in the classification cell', async () => {
    const tags = ['tag-a', 'tag-b', 'tag-c', 'tag-d'].map((name, index) => ({ id: index + 1, name }))
    listAccounts.mockResolvedValue({ items: [{ ...account, tags }], total: 1, page: 1, page_size: 20, pages: 1 })
    const wrapper = mountView()
    await flushPromises()

    const cell = wrapper.get('[data-test="taxonomy-cell"]')
    for (const tag of tags) expect(cell.text()).toContain(tag.name)
    expect(cell.text()).not.toContain('+2')
  })

  it('keeps long account names inside a fixed, truncated column', async () => {
    const longName = 'production-account-with-a-name-that-must-not-expand-the-table'
    listAccounts.mockResolvedValue({ items: [{ ...account, name: longName }], total: 1, page: 1, page_size: 20, pages: 1 })

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-test="view-table"]').attributes('data-name-class')).toContain('w-44')
    const name = wrapper.get('[data-test="view-table"] span[title]')
    expect(name.attributes('title')).toBe(longName)
    expect(name.classes()).toContain('truncate')
    expect(name.text()).toBe(longName)
  })

  it('replaces a column layout saved under an older version with the upstream defaults once', async () => {
    localStorage.setItem('account-hidden-columns', JSON.stringify(['id', 'usage', 'priority']))
    localStorage.setItem('account-hidden-columns-version', 'cockpit-console-defaults-v1')

    mountView()
    await flushPromises()

    expect(JSON.parse(localStorage.getItem('account-hidden-columns') || '[]')).toEqual([
      'today_stats', 'proxy', 'notes', 'scheduler_score', 'rate_multiplier'
    ])
    expect(localStorage.getItem('account-hidden-columns-version')).toBe('upstream-defaults-v1')
  })

  it('applies a column layout saved under the current version exactly as saved', async () => {
    localStorage.setItem('account-hidden-columns', JSON.stringify(['id', 'usage', 'priority']))
    localStorage.setItem('account-hidden-columns-version', 'upstream-defaults-v1')

    const wrapper = mountView()
    await flushPromises()

    expect(JSON.parse(localStorage.getItem('account-hidden-columns') || '[]')).toEqual(['id', 'usage', 'priority'])
    const columns = wrapper.get('[data-test="view-table"]').attributes('data-columns')?.split(',') || []
    expect(columns).not.toContain('usage')
    expect(columns).toContain('scheduler_score')
  })

  it('forwards today stats and the manual force-refresh token to non-table views', async () => {
    getBatchTodayStats.mockResolvedValue({
      stats: {
        '1': { requests: 8, tokens: 100, cost: 1, standard_cost: 1, user_cost: 2 }
      }
    })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-test="mode-compact"]').trigger('click')
    expect(wrapper.get('[data-test="view-compact"]').attributes('data-requests')).toBe('8')
    expect(wrapper.get('[data-test="view-compact"]').attributes('data-refresh-token')).toBe('0')

    await wrapper.get('[data-test="page-refresh"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="view-compact"]').attributes('data-refresh-token')).toBe('1')

    await wrapper.get('[data-test="mode-cards"]').trigger('click')
    expect(wrapper.get('[data-test="view-cards"]').attributes('data-requests')).toBe('8')
    expect(wrapper.get('[data-test="view-cards"]').attributes('data-refresh-token')).toBe('1')
  })

  it('opens details on row click and edits only after the explicit edit command', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-test="edit-modal"]').attributes('data-show')).toBe('false')
    await wrapper.get('[data-test="open-row"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="details-drawer"]').text()).toContain('console-account')
    expect(wrapper.get('[data-test="edit-modal"]').attributes('data-show')).toBe('false')

    await wrapper.get('[data-test="drawer-edit"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="edit-modal"]').attributes('data-show')).toBe('true')
    expect(wrapper.get('[data-test="edit-modal"]').attributes('data-id')).toBe('1')
  })

  it('adjusts the priority in its cell without opening the details drawer', async () => {
    updateAccount.mockReset().mockResolvedValue({ ...account, priority: 1 })
    const wrapper = mountView()
    await flushPromises()
    getById.mockClear()

    await wrapper.get('[data-testid="account-priority-increment"]').trigger('click')
    await wrapper.get('[data-testid="account-priority-value"]').trigger('click')
    await wrapper.get('[data-testid="account-priority-input"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="details-drawer"]').exists()).toBe(false)
    expect(getById).not.toHaveBeenCalled()

    // the row itself still opens the drawer
    await wrapper.get('[data-test="priority-cell"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="details-drawer"]').text()).toContain('console-account')
    wrapper.unmount()
  })

  it('uses unfiltered taxonomy counts for management and facet counts for navigation', async () => {
    getFacets.mockResolvedValue({
      total: 3,
      uncategorized_count: 3,
      platforms: [], types: [], statuses: [], plans: [], proxies: [], tags: [],
      folders: [{ id: 7, name: 'Production', sort_order: 0, account_count: 0 }]
    })
    listFolders.mockResolvedValue([{ id: 7, name: 'Production', sort_order: 0, account_count: 4 }])

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-test="folder-facet-count"]').text()).toBe('0')
    expect(wrapper.get('[data-test="folder-navigation-total"]').text()).toBe('3')
    expect(wrapper.get('[data-test="taxonomy-folder-count"]').text()).toBe('4')
  })

  it('keeps the taxonomy bar above a shrinkable account list container', async () => {
    const wrapper = mountView()
    await flushPromises()

    const taxonomy = wrapper.get('[data-test="account-taxonomy-bar"]')
    const list = wrapper.get('[data-test="account-list-scroll"]')
    expect(taxonomy.element.compareDocumentPosition(list.element) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(list.classes()).toEqual(expect.arrayContaining(['flex', 'min-h-0', 'min-w-0', 'flex-1', 'flex-col']))
  })

  it('tracks an import job without assuming synchronous account IDs', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-test="emit-import-result"]').trigger('click')
    await flushPromises()

    expect(jobTrack).toHaveBeenCalledWith({ id: 71, kind: 'account_import', status: 'pending' }, { open: false })
    expect(wrapper.get('[data-test="console-account-ids"]').text()).toBe('')
    expect(wrapper.get('[data-test="selected-ids"]').text()).toBe('')
    wrapper.unmount()
  })

  it('coalesces one account, facet, and taxonomy refresh when an import job completes', async () => {
    const wrapper = mountView()
    await flushPromises()
    const initialLists = listAccounts.mock.calls.length
    const initialFacets = getFacets.mock.calls.length
    const initialFolders = listFolders.mock.calls.length
    const initialTags = listTags.mock.calls.length

    await wrapper.get('[data-test="emit-import-result"]').trigger('click')
    await flushPromises()
    expect(listAccounts).toHaveBeenCalledTimes(initialLists)
    accountJobsState.store.recentJobs.push({ id: 71, status: 'succeeded' })

    await vi.waitFor(() => {
      expect(listAccounts).toHaveBeenCalledTimes(initialLists + 1)
      expect(getFacets).toHaveBeenCalledTimes(initialFacets + 1)
      expect(listFolders).toHaveBeenCalledTimes(initialFolders + 1)
      expect(listTags).toHaveBeenCalledTimes(initialTags + 1)
    })

    accountJobsState.store.recentJobs[0].processed_count = 1
    await flushPromises()
    expect(listAccounts).toHaveBeenCalledTimes(initialLists + 1)
    expect(getFacets).toHaveBeenCalledTimes(initialFacets + 1)
    expect(listFolders).toHaveBeenCalledTimes(initialFolders + 1)
    expect(listTags).toHaveBeenCalledTimes(initialTags + 1)
    wrapper.unmount()
  })

  it('restores URL filters through browser history while sensitive filters stay in session storage', async () => {
    sessionStorage.setItem('account-console-sensitive-filters-v1', JSON.stringify({ search: 'private search', account_ids: [1] }))
    listFolders.mockResolvedValue([{ id: 7, name: 'Production', sort_order: 0, account_count: 1 }])
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/admin/accounts', component: { template: '<div />' } }]
    })
    await router.push('/admin/accounts?folder=7&statuses=active&group_id=ungrouped&sort_by=status&sort_order=desc&page=2&page_size=50')
    await router.isReady()
    const wrapper = mountView([router])

    // the restored search text is looked up before the first list request
    await vi.waitFor(() => {
      expect(listAccounts).toHaveBeenCalledWith(2, 50, expect.objectContaining({
        folder: '7', statuses: 'active', group_id: 'ungrouped', search: 'private search', account_ids: '1', sort_by: 'status', sort_order: 'desc'
      }), expect.any(Object))
    })
    await flushPromises()
    expect(router.currentRoute.value.query.search).toBeUndefined()
    expect(router.currentRoute.value.query.account_ids).toBeUndefined()

    await router.push('/admin/accounts?statuses=inactive')
    await flushPromises()
    expect(listAccounts).toHaveBeenLastCalledWith(1, 20, expect.objectContaining({ statuses: 'inactive', search: 'private search', account_ids: '1' }), expect.any(Object))

    router.back()
    await vi.waitFor(() => {
      expect(listAccounts).toHaveBeenLastCalledWith(2, 50, expect.objectContaining({ folder: '7', statuses: 'active' }), expect.any(Object))
    })
    wrapper.unmount()
  })

  it('searches by name when the text is not the API key of any account', async () => {
    const wrapper = mountView()
    await flushPromises()
    const searches = recordListSearches()

    typeSearch(wrapper, '  console  ')
    await vi.waitFor(() => expect(searches).toEqual(['console']), SEARCH_RELOAD_WAIT)
    await flushPromises()
    expect(hasAPIKeyDigest.mock.calls).toEqual([[keyDigestTerm('console')]])
    expect(getFacets).toHaveBeenLastCalledWith(expect.objectContaining({ search: 'console' }))
    wrapper.unmount()
  })

  it('searches by digest term when the text is the API key of an account and never sends the key', async () => {
    const key = 'sk-console-upstream-key'
    hasAPIKeyDigest.mockResolvedValue(true)
    const wrapper = mountView()
    await flushPromises()
    const searches = recordListSearches()

    typeSearch(wrapper, ` ${key} `)
    await vi.waitFor(() => expect(searches).toEqual([keyDigestTerm(key)]), SEARCH_RELOAD_WAIT)
    await flushPromises()
    expect(getFacets).toHaveBeenLastCalledWith(expect.objectContaining({ search: keyDigestTerm(key) }))

    // found by its key, the account stays listed after an edit although its name does not contain the text
    const vm = wrapper.vm as any
    vm.handleAccountUpdated({ ...account, priority: 3 })
    expect(vm.accounts).toHaveLength(1)
    expect(accountRequestArguments()).not.toContain(key)
    wrapper.unmount()
  })

  it('resolves a restored search text before the first list and facets requests', async () => {
    const key = 'sk-restored-upstream-key'
    sessionStorage.setItem('account-console-sensitive-filters-v1', JSON.stringify({ search: key, account_ids: [] }))
    let answerLookup!: (inUse: boolean) => void
    hasAPIKeyDigest.mockReturnValue(new Promise<boolean>((resolve) => { answerLookup = resolve }))

    const wrapper = mountView()
    await vi.waitFor(() => expect(hasAPIKeyDigest).toHaveBeenCalledWith(keyDigestTerm(key)))
    expect(listAccounts).not.toHaveBeenCalled()
    expect(getFacets).not.toHaveBeenCalled()

    answerLookup(true)
    await flushPromises()
    expect(listAccounts).toHaveBeenCalledWith(1, expect.any(Number), expect.objectContaining({ search: keyDigestTerm(key) }), expect.any(Object))
    expect(getFacets).toHaveBeenCalledWith(expect.objectContaining({ search: keyDigestTerm(key) }))
    expect(accountRequestArguments()).not.toContain(key)
    wrapper.unmount()
  })

  it('reports a failed key lookup, puts the search box back and never sends the text', async () => {
    const text = 'sk-unresolved-upstream-key'
    const wrapper = mountView()
    await flushPromises()
    hasAPIKeyDigest.mockRejectedValue({ status: 503 })
    const searches = recordListSearches()

    typeSearch(wrapper, text)
    await vi.waitFor(() => expect(showError).toHaveBeenCalledWith('admin.accounts.failedToLoad'))
    // the filters are applied with the search the list had before, which the box shows again
    await vi.waitFor(() => expect(searches).toEqual([undefined]), SEARCH_RELOAD_WAIT)
    await flushPromises()
    expect(wrapper.findComponent(ConsoleFiltersStub).props('modelValue').search).toBe('')
    expect(JSON.parse(sessionStorage.getItem('account-console-sensitive-filters-v1') || '{}').search).toBe('')
    expect(accountRequestArguments()).not.toContain(text)
    wrapper.unmount()
  })

  it('looks a name search up again once the page adds an account, which may use that text as its key', async () => {
    const key = 'sk-added-upstream-key'
    const wrapper = mountView()
    await flushPromises()
    const searches = recordListSearches()
    typeSearch(wrapper, key)
    await vi.waitFor(() => expect(searches).toEqual([key]), SEARCH_RELOAD_WAIT)
    await flushPromises()

    hasAPIKeyDigest.mockResolvedValue(true)
    ;(wrapper.vm as any).handleAccountCreated()
    await vi.waitFor(() => expect(searches).toEqual([key, keyDigestTerm(key)]))
    await flushPromises()
    expect(getFacets).toHaveBeenLastCalledWith(expect.objectContaining({ search: keyDigestTerm(key) }))
    wrapper.unmount()
  })

  it('looks a name search up again before the list and facets reload after an import', async () => {
    const key = 'sk-imported-upstream-key'
    const wrapper = mountView()
    await flushPromises()
    const searches = recordListSearches()
    typeSearch(wrapper, key)
    await vi.waitFor(() => expect(searches).toEqual([key]), SEARCH_RELOAD_WAIT)
    await flushPromises()

    // the import adds an account that uses the text as its key; the lookup has no answer yet
    let answerLookup!: (inUse: boolean) => void
    hasAPIKeyDigest.mockReturnValue(new Promise<boolean>((resolve) => { answerLookup = resolve }))
    searches.length = 0
    getFacets.mockClear()
    await wrapper.get('[data-test="emit-import-result"]').trigger('click')
    accountJobsState.store.recentJobs.push({ id: 71, status: 'succeeded' })
    await vi.waitFor(() => expect(hasAPIKeyDigest).toHaveBeenLastCalledWith(keyDigestTerm(key)))
    await flushPromises()
    expect(searches).toEqual([])
    expect(getFacets).not.toHaveBeenCalled()

    answerLookup(true)
    await vi.waitFor(() => expect(searches).toEqual([keyDigestTerm(key)]))
    await flushPromises()
    expect(getFacets).toHaveBeenCalled()
    expect(getFacets.mock.calls.map(([filters]) => filters.search)).not.toContain(key)
    wrapper.unmount()
  })

  it('lets a filter change wait for a lookup in flight instead of sending the text it is checking', async () => {
    const key = 'sk-refreshed-upstream-key'
    const wrapper = mountView()
    await flushPromises()
    const searches = recordListSearches()
    typeSearch(wrapper, key)
    await vi.waitFor(() => expect(searches).toEqual([key]), SEARCH_RELOAD_WAIT)
    await flushPromises()

    // an account now uses the text as its key; the refresh asks again and has no answer yet
    let answerLookup!: (inUse: boolean) => void
    hasAPIKeyDigest.mockReturnValue(new Promise<boolean>((resolve) => { answerLookup = resolve }))
    searches.length = 0
    getFacets.mockClear()
    await wrapper.get('[data-test="page-refresh"]').trigger('click')
    const filters = wrapper.findComponent(ConsoleFiltersStub)
    filters.vm.$emit('update:modelValue', { ...filters.props('modelValue'), statuses: ['active'] })
    filters.vm.$emit('change')
    await flushPromises()
    expect(searches).toEqual([])
    expect(getFacets).not.toHaveBeenCalled()

    answerLookup(true)
    // the refresh loads at once, the filter change a debounce later
    await vi.waitFor(() => expect(searches).toEqual([keyDigestTerm(key), keyDigestTerm(key)]), SEARCH_RELOAD_WAIT)
    await flushPromises()
    expect(getFacets).toHaveBeenLastCalledWith(expect.objectContaining({ search: keyDigestTerm(key), statuses: 'active' }))
    wrapper.unmount()
  })

  it('asks for upstream billing rates of the current page with the filters of the list request', async () => {
    sessionStorage.setItem('account-console-sensitive-filters-v1', JSON.stringify({ search: 'console', account_ids: [] }))
    listFolders.mockResolvedValue([{ id: 7, name: 'Production', sort_order: 0, account_count: 1 }])
    listTags.mockResolvedValue([{ id: 3, name: 'canary', sort_order: 0, account_count: 1 }])
    // useTableLoader hands every list request the same params object, so the filters of a request are copied as it is made
    const listRequests: Record<string, unknown>[] = []
    listAccounts.mockImplementation(async (_page: number, _pageSize: number, filters: Record<string, unknown>) => {
      listRequests.push({ ...filters })
      return { items: [account], total: 51, page: 2, page_size: 50, pages: 2 }
    })
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/admin/accounts', component: { template: '<div />' } }]
    })
    await router.push('/admin/accounts?folder=7&statuses=active&group_id=23&tags=3&sort_by=status&sort_order=desc&page=2&page_size=50')
    await router.isReady()
    const wrapper = mountView([router])
    try {
      // the restored search text is looked up before the first list request
      await vi.waitFor(() => expect(listRequests.length).toBeGreaterThan(0))
      await flushPromises()

      await refreshBillingRates(wrapper)

      expect(getUpstreamBillingRatesWithEtag).toHaveBeenCalledTimes(1)
      const [page, pageSize, rateFilters] = getUpstreamBillingRatesWithEtag.mock.calls[0]
      expect([page, pageSize]).toEqual([2, 50])
      expect(rateFilters).toEqual({
        folder: '7', statuses: 'active', group_id: '23', tags: '3', search: 'console', sort_by: 'status', sort_order: 'desc'
      })
      for (const legacyKey of ['platform', 'type', 'status', 'group']) expect(rateFilters).not.toHaveProperty(legacyKey)
      // the filters of the list request: all it sends but lite and include_scheduler_score, which only shape its rows
      const { lite: _lite, include_scheduler_score: _includeSchedulerScore, ...listFilters } = listRequests[listRequests.length - 1]
      expect(rateFilters).toEqual(listFilters)
    } finally {
      wrapper.unmount()
    }
  })

  it('applies upstream billing rates answered for the listed accounts without requesting the list again', async () => {
    const snapshot = {
      status: 'ok',
      data: { effective_rate_multiplier: 0.065 },
      synced_rate_multiplier: 0.065,
      last_attempt_at: '2026-07-29T00:00:00Z',
      next_probe_at: '2026-07-29T00:30:00Z'
    }
    getUpstreamBillingRatesWithEtag.mockResolvedValue({
      notModified: false,
      etag: '"rates"',
      data: { items: [{ account_id: account.id, snapshot }], total: 1, page: 1, page_size: 20 }
    })
    const wrapper = mountView()
    try {
      await flushPromises()
      listAccounts.mockClear()

      await refreshBillingRates(wrapper)
      await flushPromises()

      expect(getUpstreamBillingRatesWithEtag).toHaveBeenCalledTimes(1)
      expect(listAccounts).not.toHaveBeenCalled()
      const [row] = (wrapper.vm as any).accounts
      expect(row.extra.upstream_billing_probe).toEqual(snapshot)
      expect(row.rate_multiplier).toBe(snapshot.synced_rate_multiplier)
    } finally {
      wrapper.unmount()
    }
  })

  it('asks for upstream billing rates by digest term when the search box holds the API key of an account', async () => {
    const key = 'sk-rated-upstream-key'
    hasAPIKeyDigest.mockResolvedValue(true)
    const wrapper = mountView()
    try {
      await flushPromises()
      const searches = recordListSearches()
      typeSearch(wrapper, key)
      await vi.waitFor(() => expect(searches).toEqual([keyDigestTerm(key)]), SEARCH_RELOAD_WAIT)
      await flushPromises()

      await refreshBillingRates(wrapper)

      expect(getUpstreamBillingRatesWithEtag).toHaveBeenCalledTimes(1)
      expect(getUpstreamBillingRatesWithEtag.mock.calls[0][2].search).toBe(keyDigestTerm(key))
      expect(JSON.stringify(getUpstreamBillingRatesWithEtag.mock.calls)).not.toContain(key)
    } finally {
      wrapper.unmount()
    }
  })

  it('preserves the account list and performs no reads when account testing closes', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/admin/accounts', component: { template: '<div />' } }]
    })
    await router.push('/admin/accounts?group_id=23&page=3&page_size=100&sort_by=id&sort_order=desc')
    await router.isReady()
    const rows = Array.from({ length: 100 }, (_, index) => ({ ...account, id: index + 201 }))
    listAccounts.mockResolvedValue({ items: rows, total: 400, page: 3, page_size: 100, pages: 4 })
    const wrapper = mountView([router])
    try {
      await flushPromises()
      const vm = wrapper.vm as any
      vm.toggleSel(rows[50].id)
      getById.mockResolvedValue(rows[50])
      await vm.handleTest(rows[50])
      await flushPromises()
      const modal = wrapper.findComponent(AccountTestModalStub)
      expect(modal.props('show')).toBe(true)
      const currentRows = vm.accounts
      const currentPagination = { ...vm.pagination }
      const currentFilters = JSON.stringify(vm.consoleFilters)
      const currentSort = { ...vm.sortState }
      const currentURL = router.currentRoute.value.fullPath
      const reads = [listAccounts, listWithEtag, getById, getFacets, listFolders, listTags,
        getBatchTodayStats]
      reads.forEach(read => read.mockClear())

      await wrapper.get('[data-test="close-account-test"]').trigger('click')
      await flushPromises()

      reads.forEach(read => expect(read).not.toHaveBeenCalled())
      expect(modal.props('show')).toBe(false)
      expect(modal.props('account')).toBeNull()
      expect(vm.loading).toBe(false)
      expect(vm.accounts).toBe(currentRows)
      expect(vm.pagination).toEqual(currentPagination)
      expect(vm.pagination.page).toBe(3)
      expect(JSON.stringify(vm.consoleFilters)).toBe(currentFilters)
      expect(vm.sortState).toEqual(currentSort)
      expect(router.currentRoute.value.fullPath).toBe(currentURL)
      expect(wrapper.get('[data-test="selected-ids"]').text()).toBe(String(rows[50].id))
    } finally {
      wrapper.unmount()
    }
  })

  it('defers automatic refresh for 15 seconds after account testing closes and then resumes', async () => {
    vi.useFakeTimers()
    const visibility = vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
    localStorage.setItem('account-auto-refresh', JSON.stringify({ enabled: true, interval_seconds: 5 }))
    const wrapper = mountView()
    try {
      await flushPromises()
      await (wrapper.vm as any).handleTest(account)
      await vi.advanceTimersByTimeAsync(6000)
      expect(listWithEtag).not.toHaveBeenCalled()

      await wrapper.get('[data-test="close-account-test"]').trigger('click')
      await vi.advanceTimersByTimeAsync(14999)
      expect(listWithEtag).not.toHaveBeenCalled()
      await vi.advanceTimersByTimeAsync(2001)
      expect(listWithEtag).toHaveBeenCalledTimes(1)
      expect(listAccounts).toHaveBeenCalledTimes(1)
      await vi.advanceTimersByTimeAsync(6000)
      expect(listWithEtag).toHaveBeenCalledTimes(2)
    } finally {
      wrapper.unmount()
      visibility.mockRestore()
      vi.useRealTimers()
    }
  })
})
