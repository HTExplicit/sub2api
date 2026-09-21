import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import postcss, { type Root } from 'postcss'
import tailwindcss from 'tailwindcss'
import loadConfig from 'tailwindcss/loadConfig'
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount, type VueWrapper } from '@vue/test-utils'

const routeState = vi.hoisted(() => ({
  query: {} as Record<string, unknown>,
}))
const routerPush = vi.hoisted(() => vi.fn())
const getOrder = vi.hoisted(() => vi.fn())
const paymentStore = vi.hoisted(() => ({
  config: { stripe_publishable_key: 'pk_test' } as { stripe_publishable_key?: string },
  fetchConfig: vi.fn(),
  pollOrderStatus: vi.fn(),
}))
const loadStripe = vi.hoisted(() => vi.fn())
const unexpectedNetwork = vi.hoisted(() => vi.fn(() => { throw new Error('Unexpected network in Stripe page regression') }))
const stripeElements = vi.hoisted(() => ({
  create: vi.fn(),
}))
const stripePaymentElement = vi.hoisted(() => ({
  mount: vi.fn(),
  on: vi.fn(),
}))
const stripeInstance = vi.hoisted(() => ({
  elements: vi.fn(),
  confirmPayment: vi.fn(),
  confirmAlipayPayment: vi.fn(),
  confirmWechatPayPayment: vi.fn(),
}))

vi.mock('vue-router', async () => {
  const actual = await vi.importActual<typeof import('vue-router')>('vue-router')
  return {
    ...actual,
    useRoute: () => routeState,
    useRouter: () => ({ push: routerPush }),
  }
})

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
      locale: { value: 'zh-CN' },
    }),
  }
})

vi.mock('@/stores/payment', () => ({
  usePaymentStore: () => paymentStore,
}))

vi.mock('@/api/payment', () => ({
  paymentAPI: {
    getOrder,
  },
}))

vi.mock('@stripe/stripe-js/pure', () => ({
  loadStripe,
}))

import StripePaymentView from '../StripePaymentView.vue'
import { formatPaymentAmount } from '@/components/payment/currency'
import type { PaymentOrder } from '@/types/payment'

let utilityStyles: Root
const wrappers: VueWrapper[] = []
let restoreNetwork: () => void
let initiallyDark = false

beforeAll(async () => {
  const source = readFileSync(resolve(__dirname, '../StripePaymentView.vue'), 'utf8')
  const config = loadConfig(resolve(__dirname, '../../../../tailwind.config.js'))
  utilityStyles = (await postcss([tailwindcss({ ...config, content: [{ raw: source, extension: 'vue' }] })])
    .process('@tailwind utilities;', { from: undefined })).root
})

function declarationsFor(element: Element) {
  const result: Record<string, string> = {}
  utilityStyles.walkRules(rule => {
    if (element.matches(rule.selector)) {
      rule.walkDecls(declaration => { result[declaration.prop] = declaration.value })
    }
  })
  return result
}

function orderFactory(overrides: Partial<PaymentOrder> = {}): PaymentOrder {
  return {
    id: 42,
    user_id: 7,
    amount: 100,
    pay_amount: 103,
    currency: 'CNY',
    fee_rate: 0.03,
    payment_type: 'stripe',
    out_trade_no: 'sub2_stripe_42',
    status: 'PENDING',
    order_type: 'balance',
    created_at: '2026-04-20T12:00:00Z',
    expires_at: '2026-04-20T12:30:00Z',
    refund_amount: 0,
    ...overrides,
  }
}

function mountView() {
  const wrapper = shallowMount(StripePaymentView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        Icon: true,
      },
    },
  })
  wrappers.push(wrapper)
  return wrapper
}

describe('StripePaymentView', () => {
  beforeEach(() => {
    routeState.query = {
      order_id: '42',
      client_secret: 'pi_secret_42',
    }
    routerPush.mockReset()
    getOrder.mockReset()
    getOrder.mockResolvedValue({ data: orderFactory({ currency: 'HKD' }) })
    paymentStore.config = { stripe_publishable_key: 'pk_test' }
    paymentStore.fetchConfig.mockReset().mockResolvedValue(undefined)
    paymentStore.pollOrderStatus.mockReset()
    loadStripe.mockReset().mockResolvedValue(stripeInstance)
    stripeElements.create.mockReset().mockReturnValue(stripePaymentElement)
    stripePaymentElement.mount.mockReset()
    stripePaymentElement.on.mockReset().mockImplementation((event: string, callback: () => void) => {
      if (event === 'ready') callback()
    })
    stripeInstance.elements.mockReset().mockReturnValue(stripeElements)
    stripeInstance.confirmPayment.mockReset()
    stripeInstance.confirmAlipayPayment.mockReset()
    stripeInstance.confirmWechatPayPayment.mockReset()
    window.localStorage.clear()
    initiallyDark = document.documentElement.classList.contains('dark')
    document.documentElement.classList.remove('dark')
    unexpectedNetwork.mockClear()
    vi.stubGlobal('fetch', unexpectedNetwork)
    const send = vi.spyOn(XMLHttpRequest.prototype, 'send').mockImplementation(unexpectedNetwork)
    const open = vi.spyOn(window, 'open').mockImplementation(unexpectedNetwork)
    restoreNetwork = () => { send.mockRestore(); open.mockRestore() }
  })

  afterEach(() => {
    wrappers.splice(0).forEach(wrapper => wrapper.unmount())
    restoreNetwork()
    vi.unstubAllGlobals()
    document.documentElement.classList.toggle('dark', initiallyDark)
    expect(unexpectedNetwork).not.toHaveBeenCalled()
    expect(stripeInstance.confirmPayment).not.toHaveBeenCalled()
    expect(stripeInstance.confirmAlipayPayment).not.toHaveBeenCalled()
    expect(stripeInstance.confirmWechatPayPayment).not.toHaveBeenCalled()
  })

  it('keeps the standalone light-theme amount on an opaque branded background', async () => {
    const wrapper = mountView()
    await flushPromises()
    await flushPromises()
    const header = wrapper.get('.card.overflow-hidden > div')
    expect(header.get('p.text-white').text()).toBe(formatPaymentAmount(103, 'HKD', 'zh-CN'))
    const styles = declarationsFor(header.element)
    expect(styles['background-image']).toBe('var(--theme-background-gradient-to-br, linear-gradient(to bottom right, var(--tw-gradient-stops)))')
    expect(styles['background-color']).toBe('rgb(99 91 255 / var(--tw-bg-opacity, 1))')
    expect(styles['--tw-bg-opacity']).toBe('1')
  })

  it('keeps the standalone dark-theme amount fill when the gradient is disabled', async () => {
    document.documentElement.classList.add('dark')
    const wrapper = mountView()
    await flushPromises()
    await flushPromises()
    const header = wrapper.get('.card.overflow-hidden > div')
    const styles = declarationsFor(header.element)
    const element = header.element as HTMLElement
    expect(element.style.getPropertyValue('--theme-background-gradient-to-br')).toBe('')
    element.style.setProperty('--theme-background-gradient-to-br', 'none')
    expect(styles['background-image']).toContain('var(--theme-background-gradient-to-br,')
    expect(styles['background-color']).toBe('rgb(99 91 255 / var(--tw-bg-opacity, 1))')
    expect(styles['--tw-bg-opacity']).toBe('1')
    expect(stripeInstance.elements).toHaveBeenCalledWith(expect.objectContaining({
      appearance: expect.objectContaining({ theme: 'night' }),
    }))
  })

  it('本地恢复快照缺失时使用订单接口返回的 Stripe 币种展示金额', async () => {
    getOrder.mockResolvedValue({
      data: orderFactory({ currency: 'HKD', pay_amount: 103 }),
    })

    const wrapper = mountView()
    await flushPromises()
    await flushPromises()

    expect(getOrder).toHaveBeenCalledWith(42)
    expect(loadStripe).toHaveBeenCalledWith('pk_test')
    expect(wrapper.text()).toContain(formatPaymentAmount(103, 'HKD', 'zh-CN'))
  })
})
