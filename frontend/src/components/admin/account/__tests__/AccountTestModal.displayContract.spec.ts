import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import AccountTestModal from '../AccountTestModal.vue'
import Select from '@/components/common/Select.vue'
import type { Account } from '@/types'
import contract from '../../../../../../backend/internal/service/testdata/account_available_models_contract.json'

const { getAvailableModels } = vi.hoisted(() => ({ getAvailableModels: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { getAvailableModels } } }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: vi.fn() }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))

let wrapper: VueWrapper | undefined

async function openModal() {
  wrapper = mount(AccountTestModal, {
    attachTo: document.body,
    props: {
      show: false,
      account: { id: 501, name: 'Catalog display contract', platform: 'openai', type: 'apikey', status: 'active', credentials: {} } as Account
    },
    global: {
      stubs: {
        BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
        Icon: true
      }
    }
  })
  await wrapper.setProps({ show: true })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  getAvailableModels.mockReset()
  // The same expected response is asserted against the real backend HTTP path.
  // No frontend display-name repair or Select stub is used in this test.
  getAvailableModels.mockResolvedValue(structuredClone(contract.expected))
  localStorage.setItem('auth_token', 'test-token')
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
    ok: true,
    body: { getReader: () => ({ read: vi.fn().mockResolvedValue({ done: true }) }) }
  }))
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  document.body.innerHTML = ''
  localStorage.clear()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('AccountTestModal account-model display contract', () => {
  it('renders actual selected/dropdown text for the ID-only discovery contract', async () => {
    const modal = await openModal()
    expect(getAvailableModels).toHaveBeenCalledWith(501)
    expect(modal.findAllComponents(Select)).toHaveLength(2)
    const trigger = modal.findAll('.select-trigger')[0]
    expect(trigger.text()).toContain('Case/Only-ID')
    await trigger.trigger('click')
    await nextTick()
    const labels = Array.from(document.body.querySelectorAll('.select-option-label')).map(node => node.textContent?.trim())
    expect(labels).toEqual(contract.expected.map(model => model.display_name))
    expect(labels.every(label => Boolean(label))).toBe(true)
    expect(global.fetch).not.toHaveBeenCalled()
  })

  it('searches visible text and sends the selected original ID, not its display label', async () => {
    const modal = await openModal()
    await modal.findAll('.select-trigger')[0].trigger('click')
    await nextTick()
    const search = document.body.querySelector<HTMLInputElement>('.select-search-input')!
    search.value = '保留上游显示名'
    search.dispatchEvent(new Event('input', { bubbles: true }))
    await nextTick()
    const options = document.body.querySelectorAll<HTMLElement>('[role="option"]')
    expect(options).toHaveLength(1)
    expect(options[0].textContent).toContain('保留上游显示名')
    options[0].click()
    await nextTick()
    expect(modal.findAll('.select-trigger')[0].text()).toContain('保留上游显示名')
    const start = modal.findAll('button').find(button => button.text().includes('admin.accounts.startTest'))!
    await start.trigger('click')
    await flushPromises()
    expect(global.fetch).toHaveBeenCalledTimes(1)
    const [url, request] = vi.mocked(global.fetch).mock.calls[0]
    expect(String(url)).toContain('/admin/accounts/501/test')
    expect(JSON.parse(String(request?.body)).model_id).toBe('vendor.named-v1')
    expect(contract.upstream.data[0]).toEqual({ id: 'Case/Only-ID' })
  })

  it('keeps a genuinely empty catalog empty and does not invent a test model', async () => {
    getAvailableModels.mockResolvedValue([])
    const modal = await openModal()
    expect(modal.findAll('.select-trigger')[0].text()).toContain('admin.accounts.selectTestModel')
    const start = modal.findAll('button').find(button => button.text().includes('admin.accounts.startTest'))!
    expect(start.attributes('disabled')).toBeDefined()
    expect(global.fetch).not.toHaveBeenCalled()
  })
})
