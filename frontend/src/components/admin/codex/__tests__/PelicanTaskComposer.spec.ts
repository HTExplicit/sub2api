import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PelicanTaskComposer from '../PelicanTaskComposer.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
const mocks = vi.hoisted(() => ({ list: vi.fn(), options: vi.fn(), groups: vi.fn() }))
vi.mock('@/api/admin/accounts', () => ({ accountsAPI: { list: mocks.list } }))
vi.mock('@/api/admin/groups', () => ({ groupsAPI: { getAllIncludingInactive: mocks.groups } }))
vi.mock('@/api/admin/pelicanTests', async original => ({ ...(await original<typeof import('@/api/admin/pelicanTests')>()), pelicanTestsAPI: { getAccountOptions: mocks.options } }))
vi.mock('vue-i18n', async original => ({ ...(await original<typeof import('vue-i18n')>()), useI18n: () => ({ locale: { value: 'zh' }, t: (key: string) => key }) }))
const wrappers: ReturnType<typeof mount>[] = []
beforeEach(() => {
  vi.clearAllMocks()
  mocks.groups.mockResolvedValue([])
  mocks.list.mockImplementation(async (page: number) => ({ items: Array.from({ length: 50 }, (_, index) => ({ id: (page - 1) * 50 + index + 1, name: `Account ${(page - 1) * 50 + index + 1}`, platform: 'openai', status: 'error' })), total: 850 }))
  mocks.options.mockImplementation(async (ids: number[]) => ({ accounts: ids.map(id => ({ id, name: `Account ${id}`, default_model_id: 'gpt-6.1-sol', manual_model_allowed: true, models: [
    { id: 'codex-auto-review', text_supported: false, capability_reason: 'No text generation', reasoning_efforts: [] },
    { id: 'gpt-6.1-sol', text_supported: true, reasoning_efforts: ['low', 'high'], default_effort: 'high' },
    { id: 'gpt-6-astra', text_supported: true, reasoning_efforts: ['low'], default_effort: '' }
  ] })) }))
})
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()) })
async function make(props = {}) { const wrapper = mount(PelicanTaskComposer, { props, global: { stubs: { Pagination: { props: ['page', 'total', 'pageSize'], emits: ['update:page'], template: '<div />' } } } }); wrappers.push(wrapper); await flushPromises(); return wrapper }
describe('Pelican three-step composer', () => {
  it('loads one 50-account page and retains selected accounts across pages with model-default effort', async () => {
    const wrapper = await make()
    expect(mocks.list).toHaveBeenCalledOnce()
    expect(mocks.list).toHaveBeenCalledWith(1, 50, expect.objectContaining({ status: '', platform: '' }), expect.anything())
    expect(wrapper.findAll('input[type="checkbox"]')).toHaveLength(50)
    await wrapper.get('[data-test="pelican-account-1"]').setValue(true)
    await flushPromises()
    wrapper.getComponent(Pagination).vm.$emit('update:page', 2)
    await flushPromises()
    await wrapper.get('[data-test="pelican-account-51"]').setValue(true)
    await flushPromises()
    await wrapper.get('[data-test="pelican-next"]').trigger('click')
    const models = wrapper.findAllComponents(Select).filter(model => model.props('id')?.startsWith('pelican-model-'))
    expect(models.map(model => model.props('modelValue'))).toEqual(['gpt-6.1-sol', 'gpt-6.1-sol'])
    expect(wrapper.find('datalist').exists()).toBe(false)
    expect(wrapper.findAll<HTMLSelectElement>('[data-test^="pelican-effort-"]').every(effort => effort.element.value === '')).toBe(true)
    await wrapper.get('[data-test="pelican-next"]').trigger('click')
    await wrapper.get('[data-test="pelican-test-start"]').trigger('click')
    expect(wrapper.emitted('submit')?.[0]?.[0]).toEqual({ generation_timeout_seconds: 600, test_items: ['pelican', 'candy'], scene: '', targets: [{ account_id: 1, model_id: 'gpt-6.1-sol', effort: '' }, { account_id: 51, model_id: 'gpt-6.1-sol', effort: '' }] })
  })
  it('requires correction of unsupported and duplicate combinations instead of dropping them', async () => {
    const wrapper = await make({ initial: { generation_timeout_seconds: 600, targets: [{ account_id: 1, model_id: 'codex-auto-review', effort: '' }] } })
    expect(wrapper.text()).toContain('No text generation')
    expect(wrapper.get<HTMLButtonElement>('[data-test="pelican-next"]').element.disabled).toBe(true)
    wrapper.findAllComponents(Select).find(model => model.props('id')?.startsWith('pelican-model-'))!.vm.$emit('update:modelValue', 'gpt-6.1-sol')
    await flushPromises()
    expect(wrapper.get<HTMLButtonElement>('[data-test="pelican-next"]').element.disabled).toBe(false)
    const duplicate = await make({ initial: { generation_timeout_seconds: 600, targets: [{ account_id: 1, model_id: 'gpt-6.1-sol', effort: 'low' }, { account_id: 1, model_id: 'gpt-6.1-sol', effort: 'high' }] } })
    expect(duplicate.text()).toContain('只能有一个档位')
    expect(duplicate.findAll('[data-test^="pelican-model-"]')).toHaveLength(2)
    expect(duplicate.get<HTMLButtonElement>('[data-test="pelican-next"]').element.disabled).toBe(true)
    expect(duplicate.emitted('submit')).toBeUndefined()
  })
  it('selects through the searchable dropdown with the keyboard and resets reasoning', async () => {
    const wrapper = await make({ initial: { generation_timeout_seconds: 600, targets: [{ account_id: 1, model_id: 'gpt-6.1-sol', effort: 'high' }] } })
    const trigger = wrapper.get('[data-test^="pelican-model-"] button')
    await trigger.trigger('keydown', { key: 'ArrowDown' })
    await flushPromises()
    const dropdown = document.querySelector<HTMLElement>('[role="listbox"]')!
    expect(dropdown).not.toBeNull()
    expect(dropdown.querySelector('input[type="text"]')).not.toBeNull()
    expect(dropdown.querySelector('[aria-disabled="true"]')?.textContent).toContain('No text generation')
    dropdown.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
    dropdown.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
    await flushPromises()
    const model = wrapper.findAllComponents(Select).find(item => item.props('id')?.startsWith('pelican-model-'))!
    expect(model.props('modelValue')).toBe('gpt-6-astra')
    expect(wrapper.get<HTMLSelectElement>('[data-test^="pelican-effort-"]').element.value).toBe('')
  })
  it('blocks an old model missing from the catalog without silently replacing it', async () => {
    const wrapper = await make({ initial: { generation_timeout_seconds: 600, targets: [{ account_id: 1, model_id: 'removed-model', effort: '' }] } })
    expect(wrapper.text()).toContain('admin.pelicanTests.modelUnavailable')
    expect(wrapper.get<HTMLButtonElement>('[data-test="pelican-next"]').element.disabled).toBe(true)
    expect(wrapper.emitted('submit')).toBeUndefined()
  })
})
