import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import AccountsView from '../AccountsView.vue'
import AccountBulkActionsBar from '@/components/admin/account/AccountBulkActionsBar.vue'
import BulkEditAccountModal from '@/components/account/BulkEditAccountModal.vue'

const { listAccounts, wireGet, wirePost, showError, trackJob } = vi.hoisted(() => ({
  listAccounts: vi.fn(),
  wireGet: vi.fn(),
  wirePost: vi.fn(),
  showError: vi.fn(),
  trackJob: vi.fn()
}))

vi.mock('@/api/client', () => ({
  apiClient: { get: wireGet, post: wirePost },
  default: { get: wireGet, post: wirePost }
}))
vi.mock('@/api/admin/accounts', async () => ({
  ...await vi.importActual<typeof import('@/api/admin/accounts')>('@/api/admin/accounts'),
  list: listAccounts
}))
vi.mock('@/api/admin', async () => {
  const { bulkUpdate } = await vi.importActual<typeof import('@/api/admin/accounts')>('@/api/admin/accounts')
  return {
    adminAPI: {
      accounts: {
        list: listAccounts,
        bulkUpdate,
        listWithEtag: vi.fn().mockResolvedValue({ notModified: true, etag: null, data: null }),
        getUpstreamBillingRatesWithEtag: vi.fn().mockResolvedValue({ notModified: true, etag: null, data: null }),
        getFacets: vi.fn().mockResolvedValue({ total: 3, uncategorized_count: 3, platforms: [], types: [], statuses: [], plans: [], proxies: [], folders: [], tags: [] }),
        listFolders: vi.fn().mockResolvedValue([]),
        listTags: vi.fn().mockResolvedValue([]),
        getBatchTodayStats: vi.fn().mockResolvedValue({ stats: {} }),
        getUpstreamBillingProbeSettings: vi.fn().mockResolvedValue({ enabled: false, interval_minutes: 30 }),
        getAPIKeyVisibility: vi.fn().mockResolvedValue({ enabled: false })
      },
      proxies: { getAll: vi.fn().mockResolvedValue([]) },
      groups: { getAll: vi.fn().mockResolvedValue([]) }
    }
  }
})

vi.mock('@/stores/accountJobs', () => ({
  useAccountJobsStore: () => ({ track: trackJob, reviewDuplicates: vi.fn() })
}))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showSuccess: vi.fn(), showInfo: vi.fn() })
}))
vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ token: 'test-token', user: { id: 1 } })
}))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key, locale: { value: 'en' } })
}))
vi.mock('@/components/admin/account-jobs/AccountOperationDialog.vue', () => ({
  default: { props: ['show', 'job'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' }
}))

const makeAccounts = (ids = [7, 11, 99]) => ids.map(id => ({
  id, name: `account-${id}`, platform: 'grok', type: 'oauth', status: 'active', schedulable: true,
  created_at: '2026-09-24T00:00:00Z', updated_at: '2026-09-24T00:00:00Z'
}))
const page = (ids = [7, 11, 99], total = ids.length, pages = 1) => ({
  items: makeAccounts(ids), total, page: 1, page_size: 20, pages
})
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: Error) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

let wrapper: VueWrapper | undefined
function mountView() {
  wrapper = mount(AccountsView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: { template: '<div><slot name="table" /><slot name="pagination" /></div>' },
        DataTable: {
          props: ['data'],
          template: '<div><div v-for="row in data" :key="row.id" :data-account="row.id"><slot name="cell-select" :row="row" /></div></div>'
        },
        Pagination: { emits: ['update:page'], template: '<button data-test="page-two" @click="$emit(\'update:page\', 2)">next</button>' },
        BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' },
        ConfirmDialog: true,
        ExtensionSlot: true,
        AccountFolderBar: true,
        AccountActionMenu: true,
        AccountDetailsDrawer: true,
        AccountTaxonomyManager: true,
        AccountBulkTaxonomyModal: true,
        AccountTaskDrawer: true,
        AccountTestModal: true,
        BatchTestAccountModal: true,
        AccountStatsModal: true,
        ScheduledTestsPanel: true,
        ImportDataModal: true,
        ReAuthAccountModal: true,
        SyncFromCrsModal: true,
        TempUnschedStatusModal: true,
        ErrorPassthroughRulesModal: true,
        TLSFingerprintProfilesModal: true,
        CreateAccountModal: true,
        EditAccountModal: true,
        ModelWhitelistSelector: true,
        ProxySelector: true,
        GroupSelector: true,
        Icon: true
      }
    }
  })
  return wrapper
}

function toolbarButton(view: VueWrapper, key: string) {
  const button = view.getComponent(AccountBulkActionsBar).findAll('button').find(item => item.text() === key)
  if (!button) throw new Error(`Missing real toolbar action: ${key}`)
  return button
}
async function savePriority(view: VueWrapper) {
  const modal = view.getComponent(BulkEditAccountModal)
  await modal.get('#bulk-edit-priority-enabled').setValue(true)
  await modal.get('#bulk-edit-priority').setValue(3)
  await modal.get('#bulk-edit-account-form').trigger('submit')
  await flushPromises()
}

describe('AccountsView selection scope through real bulk controls', () => {
  beforeEach(() => {
    localStorage.clear()
    sessionStorage.clear()
    vi.clearAllMocks()
    listAccounts.mockReset().mockResolvedValue(page())
    wireGet.mockReset().mockResolvedValue({ data: { items: [], total: 0 } })
    wirePost.mockReset().mockResolvedValue({ data: { id: 31, kind: 'account_bulk_update', status: 'pending' } })
  })
  afterEach(() => {
    wrapper?.unmount()
    wrapper = undefined
  })

  it.each(['admin.accounts.bulkActions.edit', 'admin.accounts.bulkEdit.submit'])(
    'submits only the frozen cross-page selection from %s', async (action) => {
      listAccounts.mockImplementation(async (currentPage: number) => currentPage === 1 ? page([7, 11], 3, 2) : page([99], 3, 2))
      const view = mountView()
      await flushPromises()
      await view.get('[data-account="7"] input').setValue(true)
      await view.get('[data-test="page-two"]').trigger('click')
      await flushPromises()
      await view.get('[data-account="99"] input').setValue(true)
      await toolbarButton(view, action).trigger('click')
      await flushPromises()
      await view.get('[data-account="99"] input').setValue(false)
      await savePriority(view)

      expect(wirePost).toHaveBeenCalledTimes(1)
      expect(wirePost).toHaveBeenCalledWith('/admin/accounts/bulk-update', { account_ids: [7, 99], priority: 3 }, expect.any(Object))
      expect(listAccounts.mock.calls.some(([, pageSize]) => pageSize === 100)).toBe(false)
      expect(trackJob).toHaveBeenCalledWith(expect.objectContaining({ id: 31 }), { open: false })
    }
  )

  it('uses a selection made while the filtered preview is loading', async () => {
    const preview = deferred<ReturnType<typeof page>>()
    listAccounts.mockImplementation((_currentPage: number, pageSize: number) => pageSize === 100 ? preview.promise : Promise.resolve(page()))
    const view = mountView()
    await flushPromises()
    await toolbarButton(view, 'admin.accounts.bulkEdit.submit').trigger('click')
    await view.get('[data-account="11"] input').setValue(true)
    preview.resolve(page())
    await flushPromises()
    await savePriority(view)

    expect(wirePost).toHaveBeenCalledTimes(1)
    expect(wirePost).toHaveBeenCalledWith('/admin/accounts/bulk-update', { account_ids: [11], priority: 3 }, expect.any(Object))
  })

  it('retains the filtered request contract when no accounts are selected', async () => {
    const view = mountView()
    await flushPromises()
    await toolbarButton(view, 'admin.accounts.bulkEdit.submit').trigger('click')
    await flushPromises()
    await savePriority(view)

    expect(wirePost).toHaveBeenCalledTimes(1)
    const [url, payload] = wirePost.mock.calls[0]
    expect(url).toBe('/admin/accounts/bulk-update')
    expect(payload).toEqual({ filters: expect.any(Object), priority: 3 })
    expect(payload).not.toHaveProperty('account_ids')
  })

  it.each(['complete', 'next-page', 'error'] as const)(
    'keeps a later manual deselection when select-all ends with %s', async outcome => {
      const allResults = deferred<ReturnType<typeof page>>()
      listAccounts.mockImplementation((_currentPage: number, pageSize: number) => pageSize === 1000 ? allResults.promise : Promise.resolve(page()))
      const view = mountView()
      await flushPromises()
      await view.get('[data-account="7"] input').setValue(true)
      await view.get('[data-account="11"] input').setValue(true)
      await toolbarButton(view, 'admin.accounts.bulkActions.selectAllResults').trigger('click')
      await view.get('[data-account="11"] input').setValue(false)
      if (outcome === 'error') allResults.reject(new Error('synthetic select-all failure'))
      else allResults.resolve(page([7, 11, 99], outcome === 'next-page' ? 1001 : 3, outcome === 'next-page' ? 2 : 1))
      await flushPromises()

      const toolbar = view.getComponent(AccountBulkActionsBar)
      expect(toolbar.props('selectedIds')).toEqual([7])
      expect(toolbar.props('selectingAll')).toBe(false)
      expect(toolbar.props('allResultsSelected')).toBe(false)
      expect(listAccounts.mock.calls.filter(([, pageSize]) => pageSize === 1000)).toHaveLength(1)
      expect(showError).not.toHaveBeenCalled()
      expect(wirePost).not.toHaveBeenCalled()
    }
  )

  it('finishes select-all normally when the selection has not changed', async () => {
    const view = mountView()
    await flushPromises()
    await toolbarButton(view, 'admin.accounts.bulkActions.selectAllResults').trigger('click')
    await flushPromises()

    const toolbar = view.getComponent(AccountBulkActionsBar)
    expect(toolbar.props('selectedIds')).toEqual([7, 11, 99])
    expect(toolbar.props('selectingAll')).toBe(false)
    expect(toolbar.props('allResultsSelected')).toBe(true)
    expect(showError).not.toHaveBeenCalled()
  })
})
