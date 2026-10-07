import { enableAutoUnmount, flushPromises, shallowMount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import ReAuthAccountModal from '../ReAuthAccountModal.vue'
import type { Account } from '@/types'

const mocks = vi.hoisted(() => ({
  exchangeCode: vi.fn(),
  applyOAuthCredentials: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn()
}))

vi.mock('@/api/admin', () => ({ adminAPI: { accounts: mocks } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => mocks }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))

enableAutoUnmount(afterEach)
beforeEach(() => vi.clearAllMocks())

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((res) => { resolve = res })
  return { promise, resolve }
}

const account = (id: number) => ({
  id,
  name: `account-${id}`,
  platform: 'anthropic',
  type: 'oauth',
  proxy_id: null,
  credentials: {}
} as unknown as Account)

const OAuthFlowStub = defineComponent({
  emits: ['cookie-auth'],
  setup(_, { expose }) {
    expose({ reset: () => {} })
    return () => null
  }
})

async function open() {
  const wrapper = shallowMount(ReAuthAccountModal, {
    props: { show: false, account: account(1) },
    global: {
      stubs: {
        BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' },
        OAuthAuthorizationFlow: OAuthFlowStub
      }
    }
  })
  await wrapper.setProps({ show: true })
  return wrapper
}

const submitSessionKey = (wrapper: VueWrapper) =>
  wrapper.getComponent(OAuthFlowStub).vm.$emit('cookie-auth', 'session-key')

describe('re-authorization requests', () => {
  it('applies the exchanged credentials to the account the dialog shows', async () => {
    mocks.exchangeCode.mockResolvedValue({ access_token: 'token' })
    mocks.applyOAuthCredentials.mockResolvedValue(account(1))
    const wrapper = await open()

    submitSessionKey(wrapper)
    await flushPromises()

    expect(mocks.applyOAuthCredentials).toHaveBeenCalledWith(1, {
      type: 'oauth',
      credentials: { access_token: 'token' },
      extra: undefined
    })
    expect(wrapper.emitted('reauthorized')).toEqual([[account(1)]])
  })

  it('does not write credentials exchanged for one account to the account opened next', async () => {
    const exchange = deferred<Record<string, unknown>>()
    mocks.exchangeCode.mockReturnValueOnce(exchange.promise)
    const wrapper = await open()

    submitSessionKey(wrapper)
    await wrapper.setProps({ show: false, account: null })
    await wrapper.setProps({ show: true, account: account(2) })
    exchange.resolve({ access_token: 'token' })
    await flushPromises()

    expect(mocks.applyOAuthCredentials).not.toHaveBeenCalled()
    expect(mocks.showSuccess).not.toHaveBeenCalled()
    expect(wrapper.emitted('reauthorized')).toBeUndefined()
  })
})
