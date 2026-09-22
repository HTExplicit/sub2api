import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import postcss, { type Root } from 'postcss'
import tailwindcss from 'tailwindcss'
import loadConfig from 'tailwindcss/loadConfig'
import { flushPromises, shallowMount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

const { loadStripe, stripe, elements, paymentElement, cancelOrder, unexpectedNetwork } = vi.hoisted(() => ({
  loadStripe: vi.fn(),
  stripe: { elements: vi.fn(), confirmPayment: vi.fn() },
  elements: { create: vi.fn() },
  paymentElement: { mount: vi.fn(), on: vi.fn() },
  cancelOrder: vi.fn(),
  unexpectedNetwork: vi.fn(() => { throw new Error('Unexpected network in Stripe contrast regression') }),
}))
vi.mock('@stripe/stripe-js/pure', () => ({ loadStripe }))
vi.mock('@/api/payment', () => ({ paymentAPI: { cancelOrder } }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showError: vi.fn() }) }))
vi.mock('vue-router', () => ({ useRouter: () => ({ resolve: vi.fn() }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

import StripePaymentInline from '../StripePaymentInline.vue'

let wrapper: VueWrapper | undefined
let utilityStyles: Root
let restoreNetwork: () => void

beforeAll(async () => {
  // Compile only this component's utilities, using the real public theme preset.
  const source = readFileSync(resolve(__dirname, '../StripePaymentInline.vue'), 'utf8')
  const config = loadConfig(resolve(__dirname, '../../../../tailwind.config.js'))
  utilityStyles = (await postcss([tailwindcss({ ...config, content: [{ raw: source, extension: 'vue' }] })])
    .process('@tailwind utilities;', { from: undefined })).root
})

beforeEach(() => {
  vi.clearAllMocks()
  loadStripe.mockResolvedValue(stripe)
  stripe.elements.mockReturnValue(elements)
  elements.create.mockReturnValue(paymentElement)
  paymentElement.on.mockImplementation((event: string, callback: () => void) => {
    if (event === 'ready') callback()
  })
  vi.stubGlobal('fetch', unexpectedNetwork)
  const send = vi.spyOn(XMLHttpRequest.prototype, 'send').mockImplementation(unexpectedNetwork)
  const open = vi.spyOn(window, 'open').mockImplementation(unexpectedNetwork)
  restoreNetwork = () => { send.mockRestore(); open.mockRestore() }
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  restoreNetwork()
  vi.unstubAllGlobals()
  expect(stripe.confirmPayment).not.toHaveBeenCalled()
  expect(cancelOrder).not.toHaveBeenCalled()
  expect(unexpectedNetwork).not.toHaveBeenCalled()
})

async function amountHeader() {
  wrapper = shallowMount(StripePaymentInline, {
    props: { orderId: 42, amount: 100, payAmount: 103, currency: 'USD', clientSecret: 'fixture-secret', publishableKey: 'pk_fixture' },
  })
  await flushPromises()
  expect(loadStripe).toHaveBeenCalledWith('pk_fixture')
  expect(paymentElement.mount).toHaveBeenCalledTimes(1)
  return wrapper.get('.card.overflow-hidden > div')
}

function declarationsFor(element: Element) {
  const declarations: Record<string, string> = {}
  utilityStyles.walkRules(rule => {
    if (element.matches(rule.selector)) {
      rule.walkDecls(declaration => { declarations[declaration.prop] = declaration.value })
    }
  })
  return declarations
}

describe('StripePaymentInline amount contrast', () => {
  it('restores the official amount gradient with a concrete solid fallback in the light theme', async () => {
    const header = await amountHeader()
    expect(header.classes()).toEqual(expect.arrayContaining(['bg-gradient-to-br', 'bg-[#635bff]', 'from-[#635bff]', 'to-[#4f46e5]']))
    expect(header.get('p.text-white').text()).toBe('$103.00')
    const declarations = declarationsFor(header.element)
    expect(declarations['background-image']).toBe('var(--theme-background-gradient-to-br, linear-gradient(to bottom right, var(--tw-gradient-stops)))')
    expect(declarations['background-color']).toBe('rgb(99 91 255 / var(--tw-bg-opacity, 1))')
    expect(declarations['--tw-bg-opacity']).toBe('1')
  })

  it('keeps the solid fill independent of the flat theme gradient token, even when the gradient is disabled', async () => {
    const header = await amountHeader()
    const theme = postcss.parse(readFileSync(resolve(__dirname, '../../../../../plugins/admin-observability/ui/assets/theme.css'), 'utf8'))
    const headerElement = header.element as HTMLElement
    let flatGradient = ''
    theme.walkDecls('--theme-background-gradient-to-br', declaration => { flatGradient = declaration.value })
    expect(flatGradient).toBe('linear-gradient(to right, var(--tw-gradient-from), var(--tw-gradient-from))')
    // No local gradient-token override: the shipped flat theme keeps ownership.
    expect(headerElement.style.getPropertyValue('--theme-background-gradient-to-br')).toBe('')
    const declarations = declarationsFor(header.element)
    for (const token of [flatGradient, 'none']) {
      // At the CSS declaration boundary only background-image uses this token.
      // The independently compiled opaque fill therefore survives either value.
      headerElement.style.setProperty('--theme-background-gradient-to-br', token)
      expect(declarations['background-image']).toContain('var(--theme-background-gradient-to-br,')
      expect(declarations['background-color']).toBe('rgb(99 91 255 / var(--tw-bg-opacity, 1))')
      expect(declarations['--tw-bg-opacity']).toBe('1')
    }
  })
})
