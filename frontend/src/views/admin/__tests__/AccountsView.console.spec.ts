import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AccountsView from '../AccountsView.vue'

const {
  listAccounts,
  listWithEtag,
  getFacets,
  getBatchTodayStats,
  getUpstreamBillingProbeSettings,
  getAllProxies,
  getAllGroups
} = vi.hoisted(() => ({
  listAccounts: vi.fn(),
  listWithEtag: vi.fn(),
  getFacets: vi.fn(),
  getBatchTodayStats: vi.fn(),
  getUpstreamBillingProbeSettings: vi.fn(),
  getAllProxies: vi.fn(),
  getAllGroups: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      list: listAccounts,
      listWithEtag,
      getFacets,
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
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showInfo: vi.fn(),
    showWarning: vi.fn()
  })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ token: 'test-token' })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
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

const DataTableStub = {
  props: ['data'],
  emits: ['row-click'],
  template: '<div data-test="view-table"><button v-for="row in data" :key="row.id" data-test="open-row" @click="$emit(\'row-click\', row)">{{ row.name }}</button></div>'
}

const DetailsDrawerStub = {
  props: ['account'],
  emits: ['edit', 'close'],
  template: '<div v-if="account" data-test="details-drawer"><span>{{ account.name }}</span><button data-test="drawer-edit" @click="$emit(\'edit\', account)">edit</button></div>'
}

const ImportDataModalStub = {
  emits: ['imported'],
  data: () => ({
    result: {
      proxy_created: 0, proxy_reused: 0, proxy_failed: 0,
      account_created: 2, account_updated: 0, account_skipped: 0, account_failed: 0,
      account_ids: [101, 102],
      items: [
        { index: 0, name: 'a', action: 'create', account_id: 101 },
        { index: 1, name: 'b', action: 'create', account_id: 102 }
      ]
    }
  }),
  template: '<button data-test="emit-import-result" @click="$emit(\'imported\', result)">imported</button>'
}

const commonStubs = {
  AppLayout: { template: '<div><slot /></div>' },
  TablePageLayout: { template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>' },
  DataTable: DataTableStub,
  AccountCompactList: { props: ['accounts'], template: '<div data-test="view-compact">{{ accounts.length }}</div>' },
  AccountCardGrid: { props: ['accounts'], template: '<div data-test="view-cards">{{ accounts.length }}</div>' },
  AccountViewModeSwitcher: ViewModeStub,
  AccountConsoleFilters: { props: ['modelValue'], template: '<div data-test="console-account-ids">{{ modelValue.account_ids.join(\',\') }}</div>' },
  AccountFolderBar: true,
  AccountTableActions: { template: '<div><slot name="beforeCreate" /><slot name="after" /></div>' },
  AccountBulkActionsBar: { props: ['selectedIds'], template: '<div data-test="selected-ids">{{ selectedIds.join(\',\') }}</div>' },
  AccountActionMenu: true,
  ImportDataModal: ImportDataModalStub,
  AccountDetailsDrawer: DetailsDrawerStub,
  EditAccountModal: { props: ['show', 'account'], template: '<div data-test="edit-modal" :data-show="String(show)" :data-id="account?.id || 0"></div>' },
  Pagination: true,
  ConfirmDialog: true,
  ReAuthAccountModal: true,
  AccountTestModal: true,
  AccountStatsModal: true,
  ScheduledTestsPanel: true,
  SyncFromCrsModal: true,
  TempUnschedStatusModal: true,
  ErrorPassthroughRulesModal: true,
  TLSFingerprintProfilesModal: true,
  CreateAccountModal: true,
  BulkEditAccountModal: true,
  AccountTaxonomyManager: true,
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

const mountView = () => mount(AccountsView, { global: { stubs: commonStubs } })

describe('admin AccountsView Cockpit console', () => {
  beforeEach(() => {
    localStorage.clear()
    listAccounts.mockReset().mockResolvedValue({ items: [account], total: 1, page: 1, page_size: 20, pages: 1 })
    listWithEtag.mockReset().mockResolvedValue({ notModified: true, etag: null, data: null })
    getFacets.mockReset().mockResolvedValue({ total: 1, platforms: [], types: [], statuses: [], plans: [], proxies: [], folders: [], tags: [] })
    getBatchTodayStats.mockReset().mockResolvedValue({ stats: {} })
    getUpstreamBillingProbeSettings.mockReset().mockResolvedValue({ enabled: true, interval_minutes: 30 })
    getAllProxies.mockReset().mockResolvedValue([])
    getAllGroups.mockReset().mockResolvedValue([])
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

  it('filters and selects successful account IDs after import', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-test="emit-import-result"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-test="console-account-ids"]').text()).toBe('101,102')
    expect(wrapper.get('[data-test="selected-ids"]').text()).toBe('101,102')
    expect(listAccounts.mock.calls.some(call => call[2]?.account_ids === '101,102')).toBe(true)
    expect(getFacets.mock.calls.some(call => call[0]?.account_ids === '101,102')).toBe(true)
  })
})
