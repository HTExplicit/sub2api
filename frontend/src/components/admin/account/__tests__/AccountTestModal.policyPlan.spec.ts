import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia } from 'pinia'
import { nextTick } from 'vue'
import { usePluginExtensions } from '@/stores/pluginExtensions'
import { ADMIN_UI_REQUEST_HEADER } from '@/api/adminUIRequest'
import type { Account, AccountTestPlanView } from '@/types'
import AccountTestModal from '../AccountTestModal.vue'

const { getAccountTestPlan } = vi.hoisted(() => ({ getAccountTestPlan: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { getAccountTestPlan } } }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: vi.fn() }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))

function plan(accountID: number, ids = ['provider/raw-a', 'provider/raw-b'], defaultID = ids[ids.length - 1] || ''): AccountTestPlanView {
  return { schema_version: 1, account_id: accountID, wire_platform: 'openai', default_mode: 'provider-view',
    models: ids.map(id => ({ id, type: 'model', created_at: '', display_name: `Label for ${id}` })),
    mode_views: { 'provider-view': { model_ids: ids, default_model_id: defaultID } } }
}
function account(id: number, platform = 'cindy'): Account {
  return { id, name: `Account ${id}`, platform, type: 'apikey', status: 'active', credentials: {} } as Account
}
let wrapper: VueWrapper | undefined
function open(target = account(88)) {
  const pinia = createPinia()
  const extensions = usePluginExtensions(pinia)
  extensions.loaded = true
  extensions.items = [] // A basic single test does not require account-tools UI.
  wrapper = mount(AccountTestModal, { attachTo: document.body, props: { show: true, account: target },
    global: { plugins: [pinia], stubs: { BaseDialog: { template: '<div><slot/><slot name="footer"/></div>' }, Icon: true } } })
  return wrapper
}
function start() { return wrapper!.findAll('button').find(button => button.text().includes('admin.accounts.startTest'))! }

beforeEach(() => {
  getAccountTestPlan.mockReset()
  localStorage.setItem('auth_token', 'local-test-token')
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, body: { getReader: () => ({ read: vi.fn().mockResolvedValue({ done: true }) }) } }))
})
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  document.body.innerHTML = ''
  localStorage.clear()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('AccountTestModal provider plan consumption', () => {
  it('renders the provider default in the real Select and sends its original ID through the unchanged SSE endpoint', async () => {
    getAccountTestPlan.mockResolvedValue(plan(88))
    const modal = open()
    await flushPromises()
    expect(modal.findAll('.select-trigger')[0].text()).toContain('Label for provider/raw-b')
    expect((modal.vm as any).selectedModelId).toBe('provider/raw-b')
    expect((modal.vm as any).isOpenAIAccount).toBe(true)
    expect(global.fetch).not.toHaveBeenCalled()
    await start().trigger('click')
    await flushPromises()
    expect(global.fetch).toHaveBeenCalledTimes(1)
    const [url, request] = vi.mocked(global.fetch).mock.calls[0]
    expect(String(url)).toContain('/admin/accounts/88/test')
    expect(request?.method).toBe('POST')
    expect(request?.headers).toMatchObject({ Authorization: 'Bearer local-test-token', [ADMIN_UI_REQUEST_HEADER]: '1' })
    expect(JSON.parse(String(request?.body))).toEqual({ model_id: 'provider/raw-b', prompt: '', mode: 'default' })
  })

  it('ignores a late plan for the previous account while keeping the new account plan', async () => {
    let resolveOld!: (value: AccountTestPlanView) => void
    getAccountTestPlan.mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
      .mockResolvedValueOnce(plan(89, ['next/raw-id']))
    const modal = open()
    await modal.setProps({ account: account(89) })
    await flushPromises()
    expect(getAccountTestPlan.mock.calls[0][1].aborted).toBe(true)
    expect(getAccountTestPlan.mock.calls.map(call => call[0])).toEqual([88, 89])
    resolveOld(plan(88))
    await flushPromises()
    expect((modal.vm as any).selectedModelId).toBe('next/raw-id')
    expect(modal.findAll('.select-trigger')[0].text()).toContain('Label for next/raw-id')
    expect((modal.vm as any).loadingModels).toBe(false)
    expect(global.fetch).not.toHaveBeenCalled()
  })

  it('cancels the model read on close and ignores a late result even before the parent hides the dialog', async () => {
    let resolveOld!: (value: AccountTestPlanView) => void
    getAccountTestPlan.mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
    const modal = open()
    ;(modal.vm as any).handleClose()
    expect(modal.emitted('close')).toHaveLength(1)
    expect(getAccountTestPlan.mock.calls[0][1].aborted).toBe(true)
    resolveOld(plan(88))
    await flushPromises()
    expect((modal.vm as any).availableModels).toEqual([])
    expect((modal.vm as any).selectedModelId).toBe('')
    expect(start().attributes('disabled')).toBeDefined()
    expect(global.fetch).not.toHaveBeenCalled()
  })

  it('requires a valid plan for Grok standalone modes but keeps their empty-model request semantics', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    getAccountTestPlan.mockResolvedValueOnce(plan(999))
    const modal = open(account(13, 'grok'))
    await flushPromises()
    ;(modal.vm as any).grokTestMode = 'search'
    await nextTick()
    expect(start().attributes('disabled')).toBeDefined()
    await (modal.vm as any).startTest()
    expect(global.fetch).not.toHaveBeenCalled()
    const emptyView = { model_ids: [], default_model_id: '' }
    getAccountTestPlan.mockResolvedValueOnce({ schema_version: 1, account_id: 14, wire_platform: 'grok', default_mode: 'text', models: [], mode_views: { text: emptyView, search: emptyView } })
    await modal.setProps({ account: account(14, 'grok') })
    await flushPromises()
    ;(modal.vm as any).grokTestMode = 'search'
    await nextTick()
    expect((modal.vm as any).canStartTest).toBe(true)
    await start().trigger('click')
    await flushPromises()
    expect(global.fetch).toHaveBeenCalledTimes(1)
    const [url, request] = vi.mocked(global.fetch).mock.calls[0]
    expect(String(url)).toContain('/admin/accounts/14/test')
    expect(JSON.parse(String(request?.body))).toMatchObject({ model_id: '', mode: 'search' })
  })
})
