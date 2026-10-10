import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PelicanTaskComposer from '../PelicanTaskComposer.vue'
import Pagination from '@/components/common/Pagination.vue'
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
    { id: 'gpt-6.1-sol', text_supported: true, reasoning_efforts: ['low', 'high'], default_effort: 'high' }
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
    const models = wrapper.findAll<HTMLInputElement>('[data-test^="pelican-model-"]')
    expect(models.map(model => model.element.value)).toEqual(['gpt-6.1-sol', 'gpt-6.1-sol'])
    expect(wrapper.findAll<HTMLSelectElement>('[data-test^="pelican-effort-"]').every(effort => effort.element.value === '')).toBe(true)
    await wrapper.get('[data-test="pelican-next"]').trigger('click')
    await wrapper.get('[data-test="pelican-test-start"]').trigger('click')
    expect(wrapper.emitted('submit')?.[0]?.[0]).toEqual({ generation_timeout_seconds: 600, targets: [{ account_id: 1, model_id: 'gpt-6.1-sol', effort: '' }, { account_id: 51, model_id: 'gpt-6.1-sol', effort: '' }] })
  })
  it('requires correction of unsupported and duplicate combinations instead of dropping them', async () => {
    const wrapper = await make({ initial: { generation_timeout_seconds: 600, targets: [{ account_id: 1, model_id: 'codex-auto-review', effort: '' }] } })
    expect(wrapper.text()).toContain('No text generation')
    expect(wrapper.get<HTMLButtonElement>('[data-test="pelican-next"]').element.disabled).toBe(true)
    await wrapper.get('[data-test^="pelican-model-"]').setValue('gpt-6.1-sol')
    expect(wrapper.get<HTMLButtonElement>('[data-test="pelican-next"]').element.disabled).toBe(false)
    const duplicate = await make({ initial: { generation_timeout_seconds: 600, targets: [{ account_id: 1, model_id: 'gpt-6.1-sol', effort: 'low' }, { account_id: 1, model_id: 'gpt-6.1-sol', effort: 'high' }] } })
    expect(duplicate.text()).toContain('只能有一个档位')
    expect(duplicate.findAll('[data-test^="pelican-model-"]')).toHaveLength(2)
    expect(duplicate.get<HTMLButtonElement>('[data-test="pelican-next"]').element.disabled).toBe(true)
    expect(duplicate.emitted('submit')).toBeUndefined()
  })
})
