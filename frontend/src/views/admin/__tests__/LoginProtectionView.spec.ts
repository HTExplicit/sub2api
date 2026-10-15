import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import LoginProtectionView from '../LoginProtectionView.vue'

enableAutoUnmount(afterEach)
const api = vi.hoisted(() => ({ getSettings: vi.fn(), saveSettings: vi.fn(), listBans: vi.fn(), unban: vi.fn() }))
vi.mock('@/api/admin/loginProtection', () => api)
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: vi.fn() }) }))
vi.mock('@/composables/useStepUp', () => ({ useStepUp: () => ({ run: (action: () => Promise<unknown>) => action() }), isStepUpCancelled: () => false }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ locale: { value: 'en' }, t: (key: string, params?: unknown) => params ? `${key} ${JSON.stringify(params)}` : key }),
}))

const settings = { enabled: true, window_minutes: 5, max_failures: 5, ban_hours: 24 }
const ban = { ip: '2001:db8::8', blocked_at: '2026-10-15T00:00:00Z', expires_at: '2026-10-16T00:00:00Z' }
function render() {
  return mount(LoginProtectionView, { global: { stubs: {
    AppLayout: { template: '<main><slot /></main>' },
    TotpStepUpDialog: true,
    Pagination: { props: ['page', 'pageSize', 'total'], emits: ['update:page', 'update:pageSize'], template: '<div><button data-test="next" @click="$emit(\'update:page\', 2)">next</button><span>{{ total }}</span></div>' },
  } } })
}
beforeEach(() => {
  vi.resetAllMocks()
  api.getSettings.mockResolvedValue({ ...settings })
  api.saveSettings.mockImplementation(async (value) => value)
  api.listBans.mockResolvedValue({ items: [ban], total: 1, page: 1, page_size: 20, pages: 1 })
  api.unban.mockResolvedValue(undefined)
})

describe('LoginProtectionView', () => {
  it('loads rules and full IPs and explains the sixth-error threshold', async () => {
    const wrapper = render()
    await flushPromises()
    expect(api.getSettings).toHaveBeenCalledOnce()
    expect(api.listBans).toHaveBeenCalledWith(1, 20)
    expect(wrapper.text()).toContain(ban.ip)
    expect(wrapper.get('[data-testid="login-protection-summary"]').text()).toContain('"trigger":6')
    await wrapper.get('#login-protection-max_failures').setValue('8')
    expect(wrapper.get('[data-testid="login-protection-summary"]').text()).toContain('"trigger":9')
  })
  it('validates range and integer values before saving', async () => {
    const wrapper = render()
    await flushPromises()
    for (const [id, value] of [['window_minutes', '0'], ['max_failures', '1.5'], ['ban_hours', '721']]) {
      await wrapper.get(`#login-protection-${id}`).setValue(value)
    }
    await wrapper.get('form').trigger('submit')
    expect(api.saveSettings).not.toHaveBeenCalled()
    expect(wrapper.findAll('input[aria-invalid="true"]')).toHaveLength(3)
  })
  it('saves explicit disabled state and selected parameters', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('#login-protection-enabled').trigger('click')
    await wrapper.get('#login-protection-ban_hours').setValue('12')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(api.saveSettings).toHaveBeenCalledWith({ ...settings, enabled: false, ban_hours: 12 })
  })
  it('shows a failed initial load and permits retry without pretending defaults were loaded', async () => {
    api.getSettings.mockRejectedValueOnce({ message: 'storage offline' })
    const wrapper = render()
    await flushPromises()
    expect(wrapper.text()).toContain('storage offline')
    expect(wrapper.find('form').exists()).toBe(false)
    await wrapper.findAll('button').find((button) => button.text() === 'admin.loginProtection.reload')!.trigger('click')
    await flushPromises()
    expect(wrapper.find('form').exists()).toBe(true)
  })
  it('paginates and unbans only the selected IP before reloading', async () => {
    const wrapper = render()
    await flushPromises()
    api.listBans.mockResolvedValue({ items: [ban], total: 21, page: 2, page_size: 20, pages: 2 })
    await wrapper.get('[data-test="next"]').trigger('click')
    await flushPromises()
    expect(api.listBans).toHaveBeenLastCalledWith(2, 20)
    await wrapper.get('tbody button').trigger('click')
    await flushPromises()
    expect(api.unban).toHaveBeenCalledWith(ban.ip)
    expect(api.listBans).toHaveBeenCalledTimes(3)
  })
  it('shows list failures instead of a false empty state', async () => {
    api.listBans.mockRejectedValue({ message: 'cannot list' })
    const wrapper = render()
    await flushPromises()
    expect(wrapper.text()).toContain('cannot list')
    expect(wrapper.text()).not.toContain('admin.loginProtection.empty')
  })
})
