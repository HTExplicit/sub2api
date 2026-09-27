import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import AccountLastUpstreamError from '../AccountLastUpstreamError.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, values?: Record<string, unknown>) => (values ? `${key} ${Object.values(values).join(' ')}` : key)
    })
  }
})

describe('AccountLastUpstreamError', () => {
  it('shows the full verbatim upstream error with its status and source in the details panel', () => {
    const message = `{"error":{"message":"${'quota exceeded '.repeat(400)}"}}`
    const wrapper = mount(AccountLastUpstreamError, {
      props: {
        variant: 'panel',
        account: { extra: { last_upstream_error: { status: 429, message, at: '2026-09-26T16:00:00Z', source: 'gateway' } } }
      }
    })

    expect(wrapper.get('pre').text()).toBe(message.trim())
    expect(wrapper.text()).toContain('HTTP 429')
    expect(wrapper.text()).toContain('admin.accounts.lastUpstreamError.source gateway')
  })

  it('opens the full text from a keyboard- and touch-reachable button in the table chip', async () => {
    const message = '{"error":{"message":"Incorrect API key provided.","code":"invalid_api_key"}}'
    const wrapper = mount(AccountLastUpstreamError, {
      props: { account: { extra: { last_upstream_error: { status: 401, message, at: '2026-09-26T16:00:00Z', source: 'gateway' } } } },
      global: { stubs: { teleport: true } }
    })

    const trigger = wrapper.get('button[aria-haspopup="dialog"]')
    expect(trigger.attributes('aria-expanded')).toBe('false')
    expect(wrapper.find('[data-test="account-last-upstream-error-message"]').exists()).toBe(false)
    await trigger.trigger('click')
    expect(trigger.attributes('aria-expanded')).toBe('true')
    expect(wrapper.get('[data-test="account-last-upstream-error-message"]').text()).toBe(message)
  })

  it('renders nothing when no upstream error was recorded', () => {
    const wrapper = mount(AccountLastUpstreamError, { props: { account: { extra: {} } } })

    expect(wrapper.find('[data-test="account-last-upstream-error"]').exists()).toBe(false)
  })
})
