import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { h } from 'vue'
import AccountsView from '../AccountsView.vue'

const {
  listAccounts,
  listWithEtag,
  getFacets,
  listFolders,
  listTags,
  getBatchTodayStats,
  getUpstreamBillingProbeSettings,
  previewCindyInsufficientDeletion,
  deleteCindyInsufficient,
  clearCindyBalanceInsufficient,
  showSuccess,
  jobTrack,
  reviewDuplicates,
  getAllProxies,
  getAllGroups
} = vi.hoisted(() => ({
  listAccounts: vi.fn(),
  listWithEtag: vi.fn(),
  getFacets: vi.fn(),
  listFolders: vi.fn(),
  listTags: vi.fn(),
  getBatchTodayStats: vi.fn(),
  getUpstreamBillingProbeSettings: vi.fn(),
  previewCindyInsufficientDeletion: vi.fn(),
  deleteCindyInsufficient: vi.fn(),
  clearCindyBalanceInsufficient: vi.fn(),
  showSuccess: vi.fn(),
  jobTrack: vi.fn(),
  reviewDuplicates: vi.fn(),
  getAllProxies: vi.fn(),
  getAllGroups: vi.fn()
}))

vi.mock('@/stores/accountJobs', () => ({
  useAccountJobsStore: () => ({ track: jobTrack, reviewDuplicates })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      list: listAccounts,
      listWithEtag,
      getFacets,
      listFolders,
      listTags,
      getBatchTodayStats,
      getUpstreamBillingProbeSettings,
      previewCindyInsufficientDeletion,
      deleteCindyInsufficient,
      clearCindyBalanceInsufficient,
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
    showError: vi.fn(),
    showSuccess,
    showInfo: vi.fn(),
    showWarning: vi.fn()
  })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ token: 'test-token' })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key === 'admin.accounts.cindyProbe.itemState.healthy'
        ? 'Luna available this run'
        : key
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
  cindy_balance_probe_job_id: 321,
  cindy_balance_probe_outcome: 'healthy',
  cindy_balance_probe_checked_at: '2032-08-16T00:02:00Z',
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

const DataTableStub = {
  props: ['data', 'columns'],
  emits: ['row-click'],
  methods: {
    columnClass(key: string) {
      return this.columns.find((column: { key: string }) => column.key === key)?.class || ''
    }
  },
  template: `
    <div data-test="view-table" :data-columns="columns.map(column => column.key).join(',')" :data-name-class="columnClass('name')">
      <div v-for="row in data" :key="row.id">
        <button data-test="open-row" @click="$emit('row-click', row)">{{ row.name }}</button>
        <slot name="cell-name" :row="row" :value="row.name" />
        <slot v-if="columns.some(column => column.key === 'cindy_probe')" name="cell-cindy_probe" :row="row" :value="row.cindy_balance_probe_outcome" />
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
    props: ['accounts', 'todayStats', 'todayStatsLoading', 'todayStatsError', 'manualRefreshToken', 'showCindyProbe'],
    template: '<div data-test="view-compact" :data-show-cindy-probe="String(showCindyProbe)" :data-refresh-token="String(manualRefreshToken)" :data-requests="String(todayStats[String(accounts[0]?.id)]?.requests ?? -1)">{{ accounts.length }}</div>'
  },
  AccountCardGrid: {
    props: ['accounts', 'todayStats', 'todayStatsLoading', 'todayStatsError', 'manualRefreshToken', 'showCindyProbe'],
    template: '<div data-test="view-cards" :data-show-cindy-probe="String(showCindyProbe)" :data-refresh-token="String(manualRefreshToken)" :data-requests="String(todayStats[String(accounts[0]?.id)]?.requests ?? -1)">{{ accounts.length }}</div>'
  },
  AccountViewModeSwitcher: ViewModeStub,
  AccountConsoleFilters: { props: ['modelValue'], template: '<div data-test="console-account-ids">{{ modelValue.account_ids.join(\',\') }}</div>' },
  AccountFolderBar: FolderBarStub,
  AccountTableActions: {
    emits: ['refresh'],
    template: '<div><button data-test="page-refresh" @click="$emit(\'refresh\')">refresh</button><slot name="beforeCreate" /><slot name="after" /></div>'
  },
  AccountBulkActionsBar: { props: ['selectedIds'], template: '<div data-test="selected-ids">{{ selectedIds.join(\',\') }}</div>' },
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
  props: { scope?: 'all' | 'cindy' } = {},
  slots: Record<string, any> = {},
) => mount(AccountsView, {
  props,
  slots,
  global: { stubs: commonStubs, plugins }
})

describe('admin AccountsView Cockpit console', () => {
  beforeEach(() => {
    localStorage.clear()
    sessionStorage.clear()
    listAccounts.mockReset().mockResolvedValue({ items: [account], total: 1, page: 1, page_size: 20, pages: 1 })
    listWithEtag.mockReset().mockResolvedValue({ notModified: true, etag: null, data: null })
    getFacets.mockReset().mockResolvedValue({ total: 1, uncategorized_count: 1, platforms: [], types: [], statuses: [], plans: [], proxies: [], folders: [], tags: [] })
    listFolders.mockReset().mockResolvedValue([])
    listTags.mockReset().mockResolvedValue([])
    getBatchTodayStats.mockReset().mockResolvedValue({ stats: {} })
    getUpstreamBillingProbeSettings.mockReset().mockResolvedValue({ enabled: true, interval_minutes: 30 })
    previewCindyInsufficientDeletion.mockReset().mockResolvedValue({ count: 2, fingerprint: 'fingerprint-2' })
    deleteCindyInsufficient.mockReset().mockResolvedValue({ id: 72, kind: 'cindy_cleanup', status: 'pending' })
    clearCindyBalanceInsufficient.mockReset().mockResolvedValue({ ...account, cindy_balance_insufficient: false })
    showSuccess.mockReset()
    jobTrack.mockReset()
    reviewDuplicates.mockReset()
    getAllProxies.mockReset().mockResolvedValue([])
    getAllGroups.mockReset().mockResolvedValue([])
  })

  it('defaults to table and persists compact/card view selection', async () => {
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.find('[data-test="view-table"]').exists()).toBe(true)

    await wrapper.get('[data-test="mode-compact"]').trigger('click')
    expect(wrapper.find('[data-test="view-compact"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="view-compact"]').attributes('data-show-cindy-probe')).toBe('false')
    expect(localStorage.getItem('account-console-view-mode')).toBe('compact')

    await wrapper.get('[data-test="mode-cards"]').trigger('click')
    expect(wrapper.find('[data-test="view-cards"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="view-cards"]').attributes('data-show-cindy-probe')).toBe('false')
    expect(localStorage.getItem('account-console-view-mode')).toBe('cards')
    wrapper.unmount()

    const restored = mountView()
    await flushPromises()
    expect(restored.find('[data-test="view-cards"]').exists()).toBe(true)
  })

  it('shows usage in the default table order', async () => {
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[data-test="view-table"]').attributes('data-columns')).toBe(
      'select,name,platform_type,usage,status,taxonomy_route,actions'
    )
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

  it('migrates only usage visibility and preserves every other saved column choice', async () => {
    localStorage.setItem('account-hidden-columns', JSON.stringify(['id', 'usage', 'priority']))
    localStorage.setItem('account-hidden-columns-version', 'cockpit-console-defaults-v1')

    mountView()
    await flushPromises()

    const hidden = JSON.parse(localStorage.getItem('account-hidden-columns') || '[]') as string[]
    expect(hidden).toContain('id')
    expect(hidden).toContain('priority')
    expect(hidden).not.toContain('usage')
    expect(localStorage.getItem('account-hidden-columns-version')).toBe('cockpit-console-defaults-v1')
    expect(localStorage.getItem('account-usage-column-version')).toBe('usage-visible-v1')
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
    expect(wrapper.get('[data-test="details-drawer"]').text()).toContain('console-account')
    expect(wrapper.get('[data-test="edit-modal"]').attributes('data-show')).toBe('false')

    await wrapper.get('[data-test="drawer-edit"]').trigger('click')
    expect(wrapper.get('[data-test="edit-modal"]').attributes('data-show')).toBe('true')
    expect(wrapper.get('[data-test="edit-modal"]').attributes('data-id')).toBe('1')
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

    expect(jobTrack).toHaveBeenCalledWith({ id: 71, kind: 'account_import', status: 'pending' })
    expect(wrapper.get('[data-test="console-account-ids"]').text()).toBe('')
    expect(wrapper.get('[data-test="selected-ids"]').text()).toBe('')
  })

  it('restores URL filters through browser history while sensitive filters stay in session storage', async () => {
    sessionStorage.setItem('account-console-sensitive-filters-v1', JSON.stringify({ search: 'private search', account_ids: [1] }))
    listFolders.mockResolvedValue([{ id: 7, name: 'Production', sort_order: 0, account_count: 1 }])
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/admin/accounts', component: { template: '<div />' } }]
    })
    await router.push('/admin/accounts?folder=7&statuses=active&group=ungrouped&sort_by=status&sort_order=desc&page=2&page_size=50')
    await router.isReady()
    const wrapper = mountView([router])
    await flushPromises()

    expect(listAccounts).toHaveBeenCalledWith(2, 50, expect.objectContaining({
      folder: '7', statuses: 'active', group: 'ungrouped', search: 'private search', account_ids: '1', sort_by: 'status', sort_order: 'desc'
    }), expect.any(Object))
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

  it('switches Cindy quick views and persists their API filters', async () => {
    getFacets.mockResolvedValue({
      total: 10, uncategorized_count: 10, cindy_total: 4, cindy_insufficient_count: 2,
      platforms: [], types: [], statuses: [], plans: [], proxies: [], folders: [], tags: []
    })
    const wrapper = mountView()
    await flushPromises()

    const viewButtons = wrapper.get('[data-test="cindy-account-view"]').findAll('button')
    expect(viewButtons).toHaveLength(3)
    expect(wrapper.get('[data-test="cindy-account-view"]').text()).toContain('admin.accounts.cindy.insufficient')

    await viewButtons[1].trigger('click')
    await flushPromises()
    expect(listAccounts.mock.calls.some(call => call[2]?.cindy_only === 'true' && call[2]?.cindy_balance_status === undefined)).toBe(true)

    await wrapper.get('[data-test="cindy-account-view"]').findAll('button')[2].trigger('click')
    await flushPromises()
    expect(listAccounts.mock.calls.some(call => call[2]?.cindy_only === 'true' && call[2]?.cindy_balance_status === 'insufficient')).toBe(true)
  })

  it('forces the dedicated Cindy scope even when the route query tries to disable it', async () => {
    getFacets.mockResolvedValue({
      total: 4, uncategorized_count: 4, cindy_total: 4, cindy_insufficient_count: 2,
      platforms: [], types: [], statuses: [], plans: [], proxies: [], folders: [], tags: []
    })
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/admin/cindy-accounts', component: { template: '<div />' } }]
    })
    await router.push('/admin/cindy-accounts?cindy_only=false')
    await router.isReady()

    const wrapper = mountView([router], { scope: 'cindy' })
    await flushPromises()

    expect(wrapper.get('[data-test="cindy-account-view"]').findAll('button')).toHaveLength(2)
    expect(listAccounts.mock.calls.some(call => call[2]?.cindy_only !== 'true')).toBe(false)
    expect(getFacets.mock.calls.some(call => call[0]?.cindy_only !== 'true')).toBe(false)
    expect(router.currentRoute.value.query.cindy_only).toBe('true')

    await router.push('/admin/cindy-accounts?cindy_only=false&cindy_balance_status=insufficient')
    await flushPromises()
    expect(listAccounts.mock.calls.at(-1)?.[2]).toEqual(expect.objectContaining({
      cindy_only: 'true',
      cindy_balance_status: 'insufficient'
    }))
  })

  it('shows recent probe data in every Cindy layout without changing the ordinary account table', async () => {
    const ordinary = mountView()
    await flushPromises()
    expect(ordinary.get('[data-test="view-table"]').attributes('data-columns')).not.toContain('cindy_probe')
    expect(ordinary.find('[data-test="cindy-probe-summary"]').exists()).toBe(false)
    ordinary.unmount()

    const cindy = mountView([], { scope: 'cindy' })
    await flushPromises()
    expect(cindy.get('[data-test="view-table"]').attributes('data-columns')).toContain('cindy_probe')
    expect(cindy.get('[data-test="cindy-probe-summary"]').text()).toContain('#321')
    expect(cindy.get('[data-test="cindy-probe-summary"]').text()).toContain('Luna available this run')

    await cindy.get('[data-test="mode-compact"]').trigger('click')
    expect(cindy.get('[data-test="view-compact"]').attributes('data-show-cindy-probe')).toBe('true')
    await cindy.get('[data-test="mode-cards"]').trigger('click')
    expect(cindy.get('[data-test="view-cards"]').attributes('data-show-cindy-probe')).toBe('true')
    cindy.unmount()
  })

  it('exposes selected IDs and exact account filters through the extension slot', async () => {
    listTags.mockResolvedValueOnce([{ id: 2, name: 'audit', account_count: 1 }])
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/admin/cindy-accounts', component: { template: '<div />' } }]
    })
    await router.push('/admin/cindy-accounts?platforms=openai&proxies=direct,3&folder=uncategorized&tags=2&group=ungrouped&privacy_mode=private&cindy_balance_status=insufficient')
    await router.isReady()

    const wrapper = mountView([router], { scope: 'cindy' }, {
      'scope-tools': ({ selectedIds, filters }: { selectedIds: number[]; filters: Record<string, unknown> }) => h('div', {
        'data-test': 'scope-tools-context',
        'data-selected': selectedIds.join(','),
        'data-filters': JSON.stringify(filters)
      })
    })
    await flushPromises()

    const context = wrapper.get('[data-test="scope-tools-context"]')
    expect(JSON.parse(context.attributes('data-filters'))).toEqual(expect.objectContaining({
      platforms: ['openai'],
      proxy_ids: [3],
      include_direct: true,
      include_uncategorized: true,
      tag_ids: [2],
      group_id: -1,
      privacy_mode: 'private',
      cindy_balance_status: 'insufficient'
    }))

    await wrapper.get('[data-test="emit-import-result"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="scope-tools-context"]').attributes('data-selected')).toBe('')
    expect(jobTrack).toHaveBeenCalledWith({ id: 71, kind: 'account_import', status: 'pending' })
  })

  it('deletes Cindy insufficient accounts only with the server preview fingerprint', async () => {
    getFacets.mockResolvedValue({
      total: 10, uncategorized_count: 10, cindy_total: 4, cindy_insufficient_count: 2,
      platforms: [], types: [], statuses: [], plans: [], proxies: [], folders: [], tags: []
    })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="cindy-account-view"]').findAll('button')[1].trigger('click')
    await flushPromises()

    const deleteButton = wrapper.get('[data-test="delete-cindy-insufficient"]')
    expect(deleteButton.attributes('disabled')).toBeUndefined()
    previewCindyInsufficientDeletion.mockClear()
    await deleteButton.trigger('click')
    await flushPromises()
    expect(previewCindyInsufficientDeletion).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-test="confirm-dialog"]').exists()).toBe(true)

    await wrapper.get('[data-test="confirm-dialog-submit"]').trigger('click')
    await flushPromises()
    expect(deleteCindyInsufficient).toHaveBeenCalledWith({ count: 2, fingerprint: 'fingerprint-2' })
    expect(jobTrack).toHaveBeenCalledWith({ id: 72, kind: 'cindy_cleanup', status: 'pending' })
    expect(showSuccess).not.toHaveBeenCalled()
  })

  it('disables Cindy cleanup when the server preview has no deletable candidates', async () => {
    getFacets.mockResolvedValue({
      total: 10, uncategorized_count: 10, cindy_total: 4, cindy_insufficient_count: 2,
      platforms: [], types: [], statuses: [], plans: [], proxies: [], folders: [], tags: []
    })
    previewCindyInsufficientDeletion.mockResolvedValue({ count: 0, fingerprint: 'empty' })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-test="cindy-account-view"]').findAll('button')[1].trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-test="delete-cindy-insufficient"]').attributes('disabled')).toBeDefined()
  })

  it('manual Cindy recovery calls the dedicated endpoint and refreshes the filtered list', async () => {
    const wrapper = mountView()
    await flushPromises()
    wrapper.findComponent({ name: 'AccountActionMenu' }).vm.$emit('recover-cindy-balance', { ...account, cindy_balance_insufficient: true })
    await flushPromises()

    expect(clearCindyBalanceInsufficient).toHaveBeenCalledWith(account.id)
    expect(showSuccess).toHaveBeenCalled()
    expect(listAccounts.mock.calls.length).toBeGreaterThan(1)
  })

  it('refreshes accounts, Cindy facets, and delete candidates when account testing closes', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="cindy-account-view"]').findAll('button')[1].trigger('click')
    await flushPromises()
    listAccounts.mockClear()
    getFacets.mockClear()
    previewCindyInsufficientDeletion.mockClear()

    await wrapper.get('[data-test="close-account-test"]').trigger('click')
    await flushPromises()

    expect(listAccounts).toHaveBeenCalled()
    expect(getFacets).toHaveBeenCalled()
    expect(previewCindyInsufficientDeletion).toHaveBeenCalled()
  })
})
