import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent, h, reactive, type Component } from 'vue'
import { createPinia } from 'pinia'
import AccountUsageCell from '@/components/account/AccountUsageCell.vue'
import AccountTestModal from '@/components/admin/account/AccountTestModal.vue'
import ReAuthAccountModal from '@/components/admin/account/ReAuthAccountModal.vue'
import AccountViewModeSwitcher from '@/components/admin/account/AccountViewModeSwitcher.vue'
import { captureAccountView, provideAccountViewContext, useAccountViewOperation } from '@/composables/useAccountViewContext'
import type { Account, AccountTestPlanView, AccountUsageInfo } from '@/types'
import { cindyAccount, viewContributions } from './accountView.fixtures'

const calls = vi.hoisted(() => ({ usage: vi.fn(), plan: vi.fn(), validateToken: vi.fn(), applyToken: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { getUsage: calls.usage, getAccountTestPlan: calls.plan, applyOAuthCredentials: calls.applyToken } } }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: vi.fn() }) }))
vi.mock('@/composables/useOpenAIOAuth', async () => {
  const { ref } = await import('vue')
  return { useOpenAIOAuth: () => ({ authUrl: ref(''), sessionId: ref(''), oauthState: ref(''), loading: ref(false), error: ref(''),
    validateRefreshToken: calls.validateToken, buildCredentials: (value: unknown) => value, buildExtraInfo: () => ({}), resetState: vi.fn() }) }
})

const deferred = <T>() => { let resolve!: (value: T) => void; const promise = new Promise<T>(done => { resolve = done }); return { promise, resolve } }
const wrappers: VueWrapper[] = []
const account = (id: number) => ({ ...cindyAccount(id), platform: 'openai', type: 'oauth' }) as Account
const usage = (utilization: number): AccountUsageInfo => ({ five_hour: { utilization } }) as AccountUsageInfo

function controllerFixture() {
  const items = reactive(viewContributions())
  const owner = items.find(item => item.slot === 'account.view.v1')!
  const state = reactive({ actor: 41, preset: 'cindy', search: 'original', available: true })
  const capture = () => captureAccountView({ contribution: owner, presetID: state.preset,
    query: { search: state.search }, actorID: state.actor, currentActor: () => state.actor, currentItems: () => items })
  return { state, items, owner, controller: { capture, available: () => state.available,
    revision: () => JSON.stringify([state.actor, state.preset, state.search, state.available, owner.package_sha256]) } }
}

function open(component: Component, props: Record<string, unknown>, fixture: ReturnType<typeof controllerFixture>, copies = 1) {
  const wrapper = mount(defineComponent({ setup() {
    provideAccountViewContext(fixture.controller)
    return () => h('div', Array.from({ length: copies }, (_, key) => h(component, { ...props, key })))
  } }), { global: { plugins: [createPinia()], stubs: {
    BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
    Select: true, Icon: true, UsageProgressBar: true, AccountQuotaInfo: true,
    AccountTestReasoningSelect: true, AccountTextTestPrompt: true, OpenAIQuotaResetCell: true, OAuthAuthorizationFlow: true,
    CNProviderBalanceCell: true, CNProviderQuotaCell: true, GrokQuotaProbeCell: true, OllamaCloudUsageCell: true
  } } })
  wrappers.push(wrapper)
  return wrapper
}

beforeEach(() => {
  calls.usage.mockReset()
  calls.plan.mockReset()
  calls.validateToken.mockReset(); calls.applyToken.mockReset()
  Object.defineProperty(window, 'matchMedia', { writable: true, value: vi.fn(() => ({ matches: true,
    addEventListener: vi.fn(), removeEventListener: vi.fn(), addListener: vi.fn(), removeListener: vi.fn() })) })
})
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); vi.unstubAllGlobals() })

describe('native account view requests', () => {
  it('honors the view layout choices and keeps all three choices for native core', async () => {
    const wrapper = mount(AccountViewModeSwitcher, { props: { modelValue: 'table' } })
    wrappers.push(wrapper)
    expect(wrapper.findAll('button')).toHaveLength(3)
    await wrapper.setProps({ modelValue: 'compact', allowedModes: ['compact'] })
    expect(wrapper.findAll('button')).toHaveLength(1)
    expect(wrapper.get('[data-test="account-view-compact"]').attributes('aria-pressed')).toBe('true')
  })
  it('drops native modal reads and failures after the target changes or the window closes', async () => {
    const fixture = controllerFixture(), state = reactive({ show: true, id: 1 })
    let operation!: ReturnType<typeof useAccountViewOperation>
    open(defineComponent({ setup() { operation = useAccountViewOperation(() => state.show, () => state.id); return () => h('div') } }), {}, fixture)
    const first = deferred<string>()
    const result = operation.read(() => first.promise)
    state.id = 2; await flushPromises()
    first.resolve('old account statistics')
    await expect(result).rejects.toThrow('Account operation changed')
    let reject!: (error: Error) => void
    const failed = operation.read(() => new Promise((_resolve, fail) => { reject = fail })).catch(error => error)
    state.show = false; await flushPromises()
    reject(new Error('old account read failed'))
    expect((await failed).message).toBe('Account operation changed')
  })
  it('never applies a late reauthorization token to a newly opened account or starts it while disabled', async () => {
    const fixture = controllerFixture(), token = deferred<Record<string, string>>()
    const props = reactive({ account: account(96023), show: true })
    calls.validateToken.mockReturnValue(token.promise)
    const wrapper = open(ReAuthAccountModal, props, fixture)
    const modal = wrapper.findComponent(ReAuthAccountModal)
    const attempt = (modal.vm as any).handleValidateRefreshToken('fixture-refresh-token')
    await flushPromises()
    props.account = account(96024)
    await flushPromises()
    token.resolve({ access_token: 'OLD-ACCOUNT-TOKEN' })
    await attempt
    expect(calls.applyToken).not.toHaveBeenCalled()
    expect(modal.emitted('reauthorized')).toBeUndefined()
    fixture.state.available = false; fixture.owner.available = false
    await flushPromises()
    await (modal.vm as any).handleValidateRefreshToken('second-token')
    expect(calls.validateToken).toHaveBeenCalledTimes(1)
  })
  it('shares only same-scope usage requests and drops old query and actor results', async () => {
    const fixture = controllerFixture(), first = deferred<AccountUsageInfo>()
    calls.usage.mockReturnValueOnce(first.promise).mockResolvedValue(usage(27))
    const wrapper = open(AccountUsageCell, { account: account(96021) }, fixture, 2)
    await flushPromises()
    expect(calls.usage).toHaveBeenCalledTimes(1)
    expect(calls.usage.mock.calls[0][3].context.query.search).toBe('original')
    fixture.state.search = 'new query'
    await flushPromises()
    expect(calls.usage).toHaveBeenCalledTimes(2)
    first.resolve(usage(99)); await flushPromises()
    for (const cell of wrapper.findAllComponents(AccountUsageCell)) {
      expect((cell.vm as any).usageInfo.five_hour.utilization).toBe(27)
      expect(cell.emitted('usage-loaded')?.flat().some((item: any) => item.five_hour.utilization === 99)).toBe(false)
    }
    fixture.state.available = false; fixture.owner.available = false
    await flushPromises()
    expect(calls.usage).toHaveBeenCalledTimes(2)
    expect((wrapper.findComponent(AccountUsageCell).vm as any).usageInfo.five_hour.utilization).toBe(27)
    const next = deferred<AccountUsageInfo>()
    calls.usage.mockReturnValueOnce(next.promise).mockResolvedValue(usage(42))
    fixture.state.available = true; fixture.owner.available = true
    await flushPromises()
    expect(calls.usage).toHaveBeenCalledTimes(2)
    fixture.state.search = 'pending actor query'
    await flushPromises()
    fixture.state.actor = 42
    await flushPromises()
    next.resolve(usage(88)); await flushPromises()
    expect(calls.usage).toHaveBeenCalledTimes(4)
    expect((wrapper.findComponent(AccountUsageCell).vm as any).usageInfo.five_hour.utilization).toBe(42)
  })

  it('keeps the modal launch scope on SSE and suppresses late content after admission is lost', async () => {
    const fixture = controllerFixture(), row = account(96022)
    const mode = { model_ids: ['fixture-model'], default_model_id: 'fixture-model' }
    calls.plan.mockResolvedValue({ schema_version: 1, account_id: row.id, wire_platform: 'openai', default_mode: 'default',
      models: [{ id: 'fixture-model', display_name: 'Fixture', type: 'model', created_at: '' }],
      mode_views: { default: mode, text: mode, compact: mode } } satisfies AccountTestPlanView)
    const chunk = deferred<ReadableStreamReadResult<Uint8Array>>()
    const reader = { read: vi.fn(() => chunk.promise), cancel: vi.fn().mockResolvedValue(undefined), releaseLock: vi.fn() }
    const fetch = vi.fn().mockResolvedValue({ ok: true, body: { getReader: () => reader } })
    vi.stubGlobal('fetch', fetch)
    const wrapper = open(AccountTestModal, { account: row, show: true }, fixture)
    await flushPromises()
    const modal = wrapper.findComponent(AccountTestModal)
    fixture.state.preset = 'banned'; fixture.state.search = 'changed after opening'
    await flushPromises()
    const run = (modal.vm as any).startTest()
    await flushPromises()
    expect(fetch).toHaveBeenCalledTimes(1)
    const request = fetch.mock.calls[0][1]
    expect(JSON.parse(request.body).view_context).toMatchObject({ preset_id: 'cindy', query: { search: 'original' } })
    expect(request.headers['X-Sub2API-Account-View']).toBeTruthy()
    fixture.state.available = false; fixture.owner.available = false
    await flushPromises()
    expect(request.signal.aborted).toBe(true)
    chunk.resolve({ done: false, value: new TextEncoder().encode('data: {"type":"content","text":"LATE-CONTENT"}\n') })
    await run
    expect((modal.vm as any).streamingContent).toBe('')
    expect((modal.vm as any).canStartTest).toBe(false)
    expect(reader.cancel).toHaveBeenCalled()
    expect(reader.releaseLock).toHaveBeenCalled()
  })
})
