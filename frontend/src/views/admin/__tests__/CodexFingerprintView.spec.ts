import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import CodexFingerprintView from '../CodexFingerprintView.vue'

const { getSettings, saveSettings, getAccount, saveAccount, list, bulkUpdate, track } = vi.hoisted(() => ({
  getSettings: vi.fn(), saveSettings: vi.fn(), getAccount: vi.fn(), saveAccount: vi.fn(), list: vi.fn(), bulkUpdate: vi.fn(), track: vi.fn()
}))
vi.mock('@/api/admin/codexFingerprint', () => ({ codexFingerprintAPI: { getSettings, saveSettings, getAccount, saveAccount } }))
vi.mock('@/api/admin/accounts', () => ({ default: { list, bulkUpdate } }))
vi.mock('@/stores/accountJobs', () => ({ useAccountJobsStore: () => ({ visibleJobs: [], track }), isTerminalAccountJob: () => false }))
vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key, locale: ref('zh') })
}))

const settings = { enabled: true, user_agent: '', client_version: '', version_auto_sync_enabled: true, synced_version: '0.161.0', effective_version: '0.161.0', effective_user_agent: 'codex_cli_rs/0.161.0 (Ubuntu 24.4.0; x86_64) xterm-256color' }
const accountView = { mode: 'full', simulation_enabled: true, identity: { user_agent: settings.effective_user_agent, originator: 'codex_cli_rs', version: '0.161.0', identity_source: 'account', identity_account_id: 1, fingerprint_mode_effective: 'full', fingerprint_reason: 'ok' } }
async function open() {
  const wrapper = mount(CodexFingerprintView, { global: { stubs: { AppLayout: { template: '<div><slot /></div>' } } } })
  await flushPromises()
  return wrapper
}

describe('CodexFingerprintView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getSettings.mockResolvedValue({ ...settings })
    saveSettings.mockImplementation(async config => ({ ...settings, ...config }))
    getAccount.mockResolvedValue({ ...accountView })
    saveAccount.mockImplementation(async (_id, mode) => ({ ...accountView, mode }))
    list.mockResolvedValue({ items: [{ id: 1, name: 'First', type: 'oauth', extra: { codex_fingerprint_mode: 'full' } }, { id: 2, name: 'Second', type: 'setup-token', extra: { codex_fingerprint_mode: 'off' } }], total: 2 })
    bulkUpdate.mockResolvedValue({ id: 8, status: 'pending' })
  })

  it('loads the effective version and saves only the four global settings without resetting account identity', async () => {
    const wrapper = await open()
    expect(wrapper.get('[data-test="fingerprint-effective-version"]').text()).toBe('0.161.0')
    await wrapper.get('[data-test="fingerprint-enabled"]').setValue(false)
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()
    expect(saveSettings).toHaveBeenCalledWith({ enabled: false, user_agent: '', client_version: '', version_auto_sync_enabled: true })
    expect(saveAccount).not.toHaveBeenCalled()
    expect(bulkUpdate).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('updates a single mode through the bounded endpoint and displays the shared effective identity', async () => {
    const wrapper = await open()
    await wrapper.get('[data-test="fingerprint-mode-1"]').setValue('session')
    await wrapper.get('[data-test="fingerprint-account-save-1"]').trigger('click')
    await flushPromises()
    expect(saveAccount).toHaveBeenCalledWith(1, 'session')
    expect(wrapper.text()).toContain(settings.effective_user_agent)
    expect(wrapper.get<HTMLButtonElement>('[data-test="fingerprint-account-save-1"]').element.disabled).toBe(true)
    await wrapper.findAll('button').find(button => button.text() === '查看生效身份')!.trigger('click')
    expect(wrapper.get<HTMLButtonElement>('[data-test="fingerprint-account-save-1"]').element.disabled).toBe(true)
    wrapper.unmount()
  })

  it('freezes selected IDs and submits an explicit off mode as the only bulk update', async () => {
    let finish!: (job: unknown) => void
    bulkUpdate.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const wrapper = await open()
    await wrapper.get('input[aria-label="选择本页账号"]').setValue(true)
    await wrapper.get('[data-test="fingerprint-bulk-mode"]').setValue('off')
    await wrapper.get('[data-test="fingerprint-bulk-save"]').trigger('click')
    await wrapper.get('input[aria-label="选择账号 First"]').setValue(false)
    expect(bulkUpdate).toHaveBeenCalledWith([1, 2], { extra: { codex_fingerprint_mode: 'off' } })
    finish({ id: 8, status: 'pending' }); await flushPromises()
    expect(track).toHaveBeenCalledWith({ id: 8, status: 'pending' })
    expect(list.mock.calls[0]?.[2]).toEqual({ platform: 'openai', types: 'oauth,setup-token', search: '', lite: '0' })
    wrapper.unmount()
  })

  it('retains an unsaved draft after storage failure and disables controls when the initial settings load fails', async () => {
    saveSettings.mockRejectedValueOnce(new Error('save failed'))
    const wrapper = await open()
    await wrapper.get('[data-test="fingerprint-enabled"]').setValue(false)
    await wrapper.find('form').trigger('submit.prevent'); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('save failed')
    expect(wrapper.get<HTMLInputElement>('[data-test="fingerprint-enabled"]').element.checked).toBe(false)
    wrapper.unmount()
    getSettings.mockRejectedValueOnce(new Error('load failed'))
    const refused = await open()
    expect(refused.get('fieldset').attributes('disabled')).toBeDefined()
    expect(refused.get('[role="alert"]').text()).toContain('load failed')
    refused.unmount()
  })
})
