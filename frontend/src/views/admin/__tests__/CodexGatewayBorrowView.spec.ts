import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import CodexGatewayBorrowView from '../CodexGatewayBorrowView.vue'
import type { AccountListItem } from '@/types'
import type { CodexGatewayBorrowConfig, CodexGatewayBorrowStatus } from '@/api/admin/codexGatewayBorrow'

const mocks = vi.hoisted(() => ({
  getConfig: vi.fn(), saveConfig: vi.fn(), getStatus: vi.fn(), prepare: vi.fn(), verify: vi.fn(), listTests: vi.fn(), getTest: vi.fn(), streamTests: vi.fn(),
  listAccounts: vi.fn(), getAvailableModels: vi.fn(), testAccount: vi.fn(), showSuccess: vi.fn()
}))
vi.mock('@/api/admin/codexGatewayBorrow', async importOriginal => ({
  ...(await importOriginal<typeof import('@/api/admin/codexGatewayBorrow')>()),
  codexGatewayBorrowAPI: mocks
}))
vi.mock('@/api/admin/accounts', () => {
  const accountsAPI = { list: mocks.listAccounts, getAvailableModels: mocks.getAvailableModels, testAccount: mocks.testAccount }
  return { accountsAPI, default: accountsAPI }
})
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: mocks.showSuccess }) }))
vi.mock('vue-i18n', async importOriginal => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => params ? `${key}:${JSON.stringify(params)}` : key, te: () => true })
}))

const wrappers: ReturnType<typeof mount>[] = []
const DRAFT_KEY = 'codex-gateway-borrow-draft'
const now = new Date().toISOString()
const disabledConfig: CodexGatewayBorrowConfig = { enabled: false, source_account_ids: [], target_account_ids: [], models: ['gpt-6-astra', 'gpt-6.1-sol'] }
const enabledConfig: CodexGatewayBorrowConfig = { ...disabledConfig, enabled: true, source_account_ids: [1], target_account_ids: [2] }
const accountError = 'Original account error\n' + 'Full account error details. '.repeat(80)
const localAccounts = [
  { id: 1, name: 'Source', platform: 'openai', type: 'oauth', status: 'active', schedulable: true, proxy_id: 7, proxy: { id: 7, name: 'Bound proxy' }, extra: { openai_oauth_responses_websockets_v2_mode: 'ctx_pool' } },
  { id: 2, name: 'Paused target', platform: 'openai', type: 'setup-token', status: 'error', schedulable: false, proxy_id: null, parent_account_id: 1, extra: {}, error_message: accountError, credentials: { model_mapping: { 'gpt-6-astra': 'gpt-6-astra' }, refresh_token: 'fixture-not-a-real-token' } },
  { id: 3, name: 'Second source', platform: 'openai', type: 'oauth', status: 'active', schedulable: true, proxy_id: null, extra: {} },
  { id: 4, name: 'API key', platform: 'openai', type: 'apikey', status: 'active' }
] as unknown as AccountListItem[]

function makeStatus(config = enabledConfig, preparing = false): CodexGatewayBorrowStatus {
  return { enabled: config.enabled, revision: 1, generated_at: now, preparing, config: structuredClone(config), sources: [], targets: [] }
}
async function mountView(config = enabledConfig) {
  mocks.getConfig.mockResolvedValue(structuredClone(config))
  const wrapper = mount(CodexGatewayBorrowView, { global: { stubs: {
    AppLayout: { template: '<div><slot /></div>' },
    CodexBorrowNav: true,
    RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' }
  } } })
  wrappers.push(wrapper)
  await flushPromises()
  return wrapper
}
function expectNoModelRequests() {
  expect(mocks.prepare).not.toHaveBeenCalled()
  expect(mocks.verify).not.toHaveBeenCalled()
  expect(mocks.streamTests).not.toHaveBeenCalled()
  expect(mocks.getAvailableModels).not.toHaveBeenCalled()
  expect(mocks.testAccount).not.toHaveBeenCalled()
  expect(mocks.listTests).not.toHaveBeenCalled()
  expect(mocks.getTest).not.toHaveBeenCalled()
}

beforeEach(() => {
  for (const mock of Object.values(mocks)) mock.mockReset()
  sessionStorage.clear()
  mocks.listAccounts.mockResolvedValue({ items: localAccounts, total: 4, page: 1, page_size: 100, pages: 1 })
  mocks.getStatus.mockResolvedValue(makeStatus())
  mocks.saveConfig.mockImplementation(async config => structuredClone(config))
})
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); sessionStorage.clear() })

describe('Codex borrow settings', () => {
  it('loads local settings and inventory only, with complete account details outside selection labels', async () => {
    const wrapper = await mountView(disabledConfig)
    expect(mocks.getConfig).toHaveBeenCalledOnce()
    expect(mocks.getStatus).toHaveBeenCalledOnce()
    expect(mocks.listAccounts).toHaveBeenCalledWith(1, 100, { platform: 'openai', types: 'oauth,setup-token' }, expect.objectContaining({ signal: expect.any(AbortSignal) }))
    expect(wrapper.find('[data-test="borrow-source_account_ids-4"]').exists()).toBe(false)
    expect(wrapper.findAll<HTMLInputElement>('input[type="checkbox"]').filter(input => input.attributes('data-test')?.match(/borrow-(source|target)_account_ids/)).every(input => !input.element.checked)).toBe(true)
    const account = wrapper.get('[data-test="borrow-account-target_account_ids-2"]')
    const details = account.get<HTMLDetailsElement>('details')
    expect(account.get('label').text()).toContain('Paused target')
    expect(account.get('label').text()).toContain('#2')
    expect(account.get('label').text()).toContain('admin.codexGatewayBorrow.paused')
    expect(account.get('label').text()).toContain('admin.codexGatewayBorrow.proxy')
    expect(details.element.closest('label')).toBeNull()
    expect(details.element.open).toBe(false)
    expect(details.get('pre').text()).toBe(accountError.trim())
    expect(details.text()).toContain('admin.codexGatewayBorrow.shadowOf')
    expect(details.text()).toContain('WS:')
    expect(details.text()).toContain('admin.codexGatewayBorrow.localMappings')
    await details.get('summary').trigger('click')
    expect(wrapper.get<HTMLInputElement>('[data-test="borrow-target_account_ids-2"]').element.checked).toBe(false)
    expect(wrapper.find('[data-test="borrow-prepare"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="borrow-test-start"]').exists()).toBe(false)
    expectNoModelRequests()
  })

  it('keeps roles disjoint, preserves source selection order and saves once without duplicate preparation', async () => {
    const wrapper = await mountView(disabledConfig)
    await wrapper.get('[data-test="borrow-enabled"]').trigger('click')
    expect(wrapper.get<HTMLButtonElement>('[data-test="borrow-save"]').element.disabled).toBe(true)
    await wrapper.get('[data-test="borrow-source_account_ids-3"]').setValue(true)
    await wrapper.get('[data-test="borrow-source_account_ids-1"]').setValue(true)
    expect(wrapper.get<HTMLInputElement>('[data-test="borrow-target_account_ids-1"]').element.disabled).toBe(true)
    expect(wrapper.get<HTMLInputElement>('[data-test="borrow-target_account_ids-3"]').element.disabled).toBe(true)
    await wrapper.get('[data-test="borrow-target_account_ids-2"]').setValue(true)
    expect(wrapper.get<HTMLInputElement>('[data-test="borrow-source_account_ids-2"]').element.disabled).toBe(true)
    mocks.getStatus.mockResolvedValue(makeStatus(enabledConfig, true))
    await wrapper.get('[data-test="borrow-save"]').trigger('click')
    await flushPromises()
    expect(mocks.saveConfig).toHaveBeenCalledOnce()
    expect(mocks.saveConfig.mock.calls[0][0]).toEqual({ enabled: true, source_account_ids: [3, 1], target_account_ids: [2], models: ['gpt-6-astra', 'gpt-6.1-sol'] })
    expect(mocks.getStatus).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-test="borrow-save-result"]').text()).toContain('admin.codexGatewayBorrow.saved')
    expect(wrapper.get('[data-test="borrow-save-result"]').text()).toContain('admin.codexGatewayBorrow.preparing')
    expect(wrapper.get('[data-test="borrow-status-link"]').attributes('href')).toBe('/admin/codex-gateway-borrow/status')
    expect(sessionStorage.getItem(DRAFT_KEY)).toBeNull()
    expectNoModelRequests()
  })

  it('retains config-only unsaved drafts across remount and leaves saved status unchanged on refresh', async () => {
    const wrapper = await mountView()
    await wrapper.get('[data-test="borrow-source_account_ids-3"]').setValue(true)
    await wrapper.get('[data-test="borrow-config-model-gpt-6-astra"]').setValue(false)
    const stored = JSON.parse(sessionStorage.getItem(DRAFT_KEY)!)
    expect(stored).toEqual({ version: 1, base: enabledConfig, draft: { ...enabledConfig, source_account_ids: [1, 3], models: ['gpt-6.1-sol'] } })
    expect(sessionStorage.getItem(DRAFT_KEY)).not.toContain('fixture-not-a-real-token')
    await wrapper.get('[data-test="borrow-refresh"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="borrow-saved-configuration"]').text()).toContain('"sources":1,"targets":1,"models":2')
    expect(wrapper.get<HTMLInputElement>('[data-test="borrow-source_account_ids-3"]').element.checked).toBe(true)
    wrapper.unmount()
    const reopened = await mountView()
    expect(mocks.getConfig).toHaveBeenCalledTimes(2)
    expect(reopened.get<HTMLInputElement>('[data-test="borrow-source_account_ids-3"]').element.checked).toBe(true)
    expect(reopened.get<HTMLInputElement>('[data-test="borrow-config-model-gpt-6-astra"]').element.checked).toBe(false)
    expect(reopened.find('[data-test="borrow-unsaved"]').exists()).toBe(true)
    expect(reopened.get('[data-test="borrow-saved-configuration"]').text()).toContain('"sources":1,"targets":1,"models":2')
    expect(mocks.saveConfig).not.toHaveBeenCalled()
    expectNoModelRequests()
    await reopened.get('[data-test="borrow-reset"]').trigger('click')
    expect(reopened.get<HTMLInputElement>('[data-test="borrow-source_account_ids-3"]').element.checked).toBe(false)
    expect(reopened.get<HTMLInputElement>('[data-test="borrow-config-model-gpt-6-astra"]').element.checked).toBe(true)
    expect(sessionStorage.getItem(DRAFT_KEY)).toBeNull()
  })

  it('discards a draft when the saved backend baseline has changed', async () => {
    sessionStorage.setItem(DRAFT_KEY, JSON.stringify({ version: 1, base: enabledConfig, draft: { ...enabledConfig, source_account_ids: [1, 3] } }))
    const changed = { ...enabledConfig, models: ['gpt-6.1-sol'] }
    const wrapper = await mountView(changed)
    expect(wrapper.get<HTMLInputElement>('[data-test="borrow-source_account_ids-3"]').element.checked).toBe(false)
    expect(wrapper.get<HTMLInputElement>('[data-test="borrow-config-model-gpt-6-astra"]').element.checked).toBe(false)
    expect(wrapper.find('[data-test="borrow-unsaved"]').exists()).toBe(false)
    expect(sessionStorage.getItem(DRAFT_KEY)).toBeNull()
    expect(mocks.saveConfig).not.toHaveBeenCalled()
    expectNoModelRequests()
  })

  it('reports a successful save separately from a failed status read', async () => {
    const wrapper = await mountView()
    await wrapper.get('[data-test="borrow-source_account_ids-3"]').setValue(true)
    mocks.getStatus.mockRejectedValue(new Error('Status read unavailable'))
    await wrapper.get('[data-test="borrow-save"]').trigger('click')
    await flushPromises()
    expect(mocks.saveConfig).toHaveBeenCalledOnce()
    expect(mocks.showSuccess).toHaveBeenCalledWith('admin.codexGatewayBorrow.saved')
    expect(wrapper.get('[data-test="borrow-save-result"]').text()).toContain('admin.codexGatewayBorrow.saved')
    expect(wrapper.get('[data-test="borrow-save-result"]').text()).toContain('admin.codexGatewayBorrow.statusReadFailed')
    expect(wrapper.find('[data-test="borrow-preparation-state"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="borrow-unsaved"]').exists()).toBe(false)
    expect(sessionStorage.getItem(DRAFT_KEY)).toBeNull()
    expectNoModelRequests()
  })

  it('cancels local reads when leaving the settings page without saving', async () => {
    const wrapper = await mountView()
    await wrapper.get('[data-test="borrow-source_account_ids-3"]').setValue(true)
    const configSignal = mocks.getConfig.mock.calls[0][0] as AbortSignal
    const inventorySignal = mocks.listAccounts.mock.calls[0][3].signal as AbortSignal
    wrapper.unmount()
    expect(configSignal.aborted).toBe(true)
    expect(inventorySignal.aborted).toBe(true)
    expect(sessionStorage.getItem(DRAFT_KEY)).not.toBeNull()
    expect(mocks.saveConfig).not.toHaveBeenCalled()
    expectNoModelRequests()
  })
})
