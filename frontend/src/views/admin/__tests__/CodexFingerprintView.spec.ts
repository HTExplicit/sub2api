import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import CodexFingerprintView from '../CodexFingerprintView.vue'
import type { CodexFingerprintAccountView, CodexFingerprintMode } from '@/api/admin/codexFingerprint'

let language = 'zh'
let simulationEnabled = true
const serverModes: Record<number, CodexFingerprintMode> = {}

const { getSettings, saveSettings, getAccount, saveAccount, list, bulkUpdate, track } = vi.hoisted(() => ({
  getSettings: vi.fn(), saveSettings: vi.fn(), getAccount: vi.fn(), saveAccount: vi.fn(), list: vi.fn(), bulkUpdate: vi.fn(), track: vi.fn()
}))
vi.mock('@/api/admin/codexFingerprint', () => ({ codexFingerprintAPI: { getSettings, saveSettings, getAccount, saveAccount } }))
vi.mock('@/api/admin/accounts', () => ({ default: { list, bulkUpdate } }))
vi.mock('@/stores/accountJobs', () => ({ useAccountJobsStore: () => ({ visibleJobs: [], track }), isTerminalAccountJob: () => false }))
vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key, locale: ref(language) })
}))

const settings = { enabled: true, user_agent: '', client_version: '', version_auto_sync_enabled: true, synced_version: '0.161.0', effective_version: '0.161.0', effective_user_agent: 'codex_cli_rs/0.161.0 (Ubuntu 24.4.0; x86_64) xterm-256color' }
const accountView: CodexFingerprintAccountView = {
  mode: 'full', simulation_enabled: true,
  identity: { user_agent: 'codex-tui/0.161.0 (Windows 10.0.26100; x86_64) vscode/1.104.0 (codex-tui; 0.161.0)', originator: 'codex-tui', version: '0.161.0', identity_source: 'account', identity_account_id: 1, identity_persisted: true, fingerprint_mode_configured: 'full', fingerprint_mode_effective: 'full', fingerprint_reason: 'ok' },
  device_identity: { v: 1, os_type: 'Windows', os_version: '10.0.26100', arch: 'x86_64', terminal: 'vscode/1.104.0', sandbox: 'windows_sandbox', generated_at: '2026-10-08T08:00:00Z' },
  effective_device: { os_type: 'Windows', os_version: '10.0.26100', arch: 'x86_64', terminal: 'vscode/1.104.0', platform_sandbox: 'windows_sandbox' },
  identifier_policy: {
    installation_id: { behavior: 'fixed', rule: 'account_device', value: 'fixed-installation' },
    session_id: { behavior: 'fixed', rule: 'account_session', value: 'fixed-session' },
    thread_id: { behavior: 'fixed', rule: 'account_thread', value: 'fixed-session' },
    parent_thread_id: { behavior: 'request_derived', rule: 'remove_parent_self_reference', value: null },
    window_id: { behavior: 'request_derived', rule: 'merged_window', value: null }
  }
}
function viewFor(id: number, mode: CodexFingerprintMode = serverModes[id] || 'device'): CodexFingerprintAccountView {
  const view = structuredClone(accountView)
  view.mode = mode; view.simulation_enabled = simulationEnabled
  view.identity.identity_account_id = id; view.identity.fingerprint_mode_configured = mode
  view.identity.fingerprint_mode_effective = simulationEnabled ? mode : 'off'
  if (!simulationEnabled) {
    view.effective_device = null; view.identity.identity_source = 'protocol_fallback'; view.identity.fingerprint_reason = 'simulation_disabled'
    view.identity.user_agent = settings.effective_user_agent
  }
  if (mode === 'off' || !simulationEnabled) {
    for (const key of Object.keys(view.identifier_policy) as Array<keyof typeof view.identifier_policy>) view.identifier_policy[key] = { behavior: 'passthrough', rule: 'preserve_client', value: null }
  } else if (mode !== 'full') {
    view.identifier_policy.thread_id = { behavior: mode === 'session' ? 'request_derived' : 'passthrough', rule: mode === 'session' ? 'mapped_thread' : 'preserve_client', value: null }
  }
  return view
}
async function open() {
  const wrapper = mount(CodexFingerprintView, { props: { advanced: true }, global: { stubs: { CodexLayout: { template: '<div><slot /></div>' }, CodexRuntimeSettings: true, AppLayout: { template: '<div><slot /></div>' } } } })
  await flushPromises()
  return wrapper
}

describe('CodexFingerprintView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    language = 'zh'; simulationEnabled = true
    for (const id of Object.keys(serverModes)) delete serverModes[Number(id)]
    serverModes[1] = 'full'; serverModes[2] = 'off'
    getSettings.mockResolvedValue({ ...settings })
    saveSettings.mockImplementation(async config => { simulationEnabled = config.enabled; return { ...settings, ...config } })
    getAccount.mockImplementation(async id => viewFor(id))
    saveAccount.mockImplementation(async (id, mode) => { serverModes[id] = mode; return viewFor(id, mode) })
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
    await wrapper.get('[data-test="fingerprint-account-refresh-1"]').trigger('click'); await flushPromises()
    expect(wrapper.get<HTMLButtonElement>('[data-test="fingerprint-account-save-1"]').element.disabled).toBe(true)
    wrapper.unmount()
  })

  it('automatically displays the complete persisted device, current policy and fixed identifiers', async () => {
    const wrapper = await open()
    expect(getAccount).toHaveBeenCalledWith(1, { signal: expect.any(AbortSignal) })
    const identity = wrapper.findAll('[data-test="fingerprint-identity"]')[0]!
    for (const value of ['固定设备身份', '已持久保存', 'Windows', '10.0.26100', 'x86_64', 'vscode/1.104.0', 'windows_sandbox', '生成时间', '身份结构版本', '当前策略，适用于新请求', '保存的收敛模式', '生效收敛模式']) expect(identity.text()).toContain(value)
    expect(identity.get('[data-test="fingerprint-installation_id"]').text()).toBe('fixed-installation')
    expect(identity.text()).toContain('保留请求中的窗口序号')
    expect(wrapper.text()).not.toContain('查看生效身份')
    expect(wrapper.get('[data-test="fingerprint-account-refresh-1"]').attributes('aria-label')).toBe('刷新账号身份 First')
    wrapper.unmount()
  })

  it('limits identity reads to three including obsolete requests that have not settled', async () => {
    list.mockResolvedValue({ items: Array.from({ length: 6 }, (_, i) => ({ id: i + 1, name: 'Account ' + (i + 1), type: 'oauth', extra: {} })), total: 6 })
    const pending: Array<(view: CodexFingerprintAccountView) => void> = []
    let active = 0, maximum = 0
    getAccount.mockImplementation(() => {
      active++; maximum = Math.max(maximum, active)
      return new Promise<CodexFingerprintAccountView>(resolve => pending.push(resolve)).finally(() => { active-- })
    })
    const wrapper = await open()
    expect(getAccount).toHaveBeenCalledTimes(3)
    await wrapper.findAll('button').find(button => button.text() === '刷新账号')!.trigger('click'); await flushPromises()
    expect(getAccount).toHaveBeenCalledTimes(3)
    pending[0]!(viewFor(1)); await flushPromises()
    expect(getAccount).toHaveBeenCalledTimes(4)
    expect(maximum).toBe(3)
    wrapper.unmount()
    for (const finish of pending) finish(viewFor(1))
    await flushPromises()
  })

  it('isolates a failed identity read and retries the row without overwriting a draft edited during the read', async () => {
    let finish!: (view: CodexFingerprintAccountView) => void
    getAccount.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const wrapper = await open()
    await wrapper.get('[data-test="fingerprint-mode-1"]').setValue('session')
    finish(viewFor(1)); await flushPromises()
    expect(wrapper.get<HTMLSelectElement>('[data-test="fingerprint-mode-1"]').element.value).toBe('session')
    getAccount.mockRejectedValueOnce(new Error('identity read failed'))
    await wrapper.get('[data-test="fingerprint-account-refresh-1"]').trigger('click'); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('identity read failed')
    expect(wrapper.findAll('[data-test="fingerprint-identity"]')).toHaveLength(2)
    await wrapper.get('[data-test="fingerprint-account-retry-1"]').trigger('click'); await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.get<HTMLSelectElement>('[data-test="fingerprint-mode-1"]').element.value).toBe('session')
    await wrapper.findAll('button').find(button => button.text() === '刷新账号')!.trigger('click'); await flushPromises()
    expect(wrapper.get<HTMLSelectElement>('[data-test="fingerprint-mode-1"]').element.value).toBe('session')
    wrapper.unmount()
  })

  it('rejects a late identity response after changing pages', async () => {
    let finish!: (view: CodexFingerprintAccountView) => void
    list.mockImplementation(async page => ({ items: [{ id: page, name: page === 1 ? 'First' : 'Second', type: 'oauth', extra: {} }], total: 40 }))
    getAccount.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const wrapper = await open()
    const signal = getAccount.mock.calls[0]![1].signal as AbortSignal
    await wrapper.findAll('button').find(button => button.text() === '下一页')!.trigger('click'); await flushPromises()
    expect(signal.aborted).toBe(true)
    const obsolete = viewFor(1); obsolete.identity.user_agent = 'obsolete-page-identity'
    finish(obsolete); await flushPromises()
    expect(wrapper.text()).toContain('Second')
    expect(wrapper.text()).not.toContain('obsolete-page-identity')
    expect(wrapper.findAll('[data-test="fingerprint-identity"]')).toHaveLength(1)
    wrapper.unmount()
  })

  it('reloads identities after a global save and rejects the old policy even if cancellation is ignored', async () => {
    let finish!: (view: CodexFingerprintAccountView) => void
    getAccount.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const wrapper = await open()
    await wrapper.get('[data-test="fingerprint-enabled"]').setValue(false)
    await wrapper.find('form').trigger('submit.prevent'); await flushPromises()
    expect(wrapper.findAll('[data-test="fingerprint-client-identity"]')).toHaveLength(2)
    const obsolete = structuredClone(accountView); obsolete.identity.user_agent = 'obsolete-policy-identity'
    finish(obsolete); await flushPromises()
    expect(wrapper.text()).not.toContain('obsolete-policy-identity')
    expect(wrapper.findAll('[data-test="fingerprint-client-identity"]')).toHaveLength(2)
    expect(wrapper.text()).toContain('已持久保存')
    wrapper.unmount()
  })

  it('does not let an identity read overwrite a subsequently saved mode', async () => {
    let finish!: (view: CodexFingerprintAccountView) => void
    getAccount.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const wrapper = await open()
    await wrapper.get('[data-test="fingerprint-mode-1"]').setValue('session')
    await wrapper.get('[data-test="fingerprint-account-save-1"]').trigger('click'); await flushPromises()
    const obsolete = structuredClone(accountView); obsolete.identity.user_agent = 'obsolete-mode-identity'
    finish(obsolete); await flushPromises()
    expect(wrapper.get<HTMLSelectElement>('[data-test="fingerprint-mode-1"]').element.value).toBe('session')
    expect(wrapper.get<HTMLButtonElement>('[data-test="fingerprint-account-save-1"]').element.disabled).toBe(true)
    expect(wrapper.text()).not.toContain('obsolete-mode-identity')
    wrapper.unmount()
  })

  it('shows an explicitly missing identity instead of blank fields that imply a saved device', async () => {
    getAccount.mockImplementation(async id => {
      const view = viewFor(id)
      view.device_identity = null; view.identity.identity_persisted = false
      view.identity.fingerprint_reason = 'seed_missing'; view.identity.fingerprint_mode_effective = 'off'
      return view
    })
    const wrapper = await open()
    expect(wrapper.findAll('[data-test="fingerprint-persistence"]')[0]!.text()).toBe('身份缺失')
    expect(wrapper.text()).toContain('身份种子缺失，未收敛')
    wrapper.unmount()
  })

  it('shows English override, parent source, unknown UA fields and an unpersisted identity without inventing a time', async () => {
    language = 'en'
    getAccount.mockImplementation(async id => {
      const view = viewFor(id)
      view.identity.identity_source = 'override_ua'
      if (id === 1) {
        view.identity.identity_account_id = 9
        view.effective_device = { os_type: 'Mac OS X', os_version: '14.0', arch: 'arm64', terminal: 'iTerm.app/3.5.15', platform_sandbox: 'seatbelt' }
      } else {
        view.effective_device = { os_type: null, os_version: null, arch: null, terminal: null, platform_sandbox: null }
        view.identity.identity_persisted = false
        delete view.device_identity!.generated_at
      }
      return view
    })
    const wrapper = await open()
    for (const value of ['Fixed device identity', 'account UA override replaces', 'parent account identity', 'Mac OS X', 'Windows', 'Cannot determine from UA', 'Derived from the seed', 'Not recorded']) expect(wrapper.text()).toContain(value)
    expect(wrapper.get('[data-test="fingerprint-account-refresh-1"]').attributes('aria-label')).toBe('Refresh account identity First')
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
    expect(wrapper.find('[data-test="fingerprint-client-identity"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-test="fingerprint-identity"]')).toHaveLength(2)
    wrapper.unmount()
    getSettings.mockRejectedValueOnce(new Error('load failed'))
    const refused = await open()
    expect(refused.get('fieldset').attributes('disabled')).toBeDefined()
    expect(refused.get('[role="alert"]').text()).toContain('load failed')
    refused.unmount()
  })
})
