import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import BatchTestAccountModal from '../BatchTestAccountModal.vue'
import AccountTestReasoningSelect from '../AccountTestReasoningSelect.vue'
const { batchTestModels, batchTest } = vi.hoisted(() => ({ batchTestModels: vi.fn(), batchTest: vi.fn() }))
vi.mock('../api', () => ({ accountJobsAPI: { batchTestModels, batchTest } }))
vi.mock('@sub2api/plugin-ui', async () => { const actual = await vi.importActual<typeof import('@sub2api/plugin-ui')>('@sub2api/plugin-ui'); const { ref } = await import('vue'); return { ...actual, useNotifications: () => ({ showError: vi.fn() }), usePersistentDraft: () => ref('') } })
vi.mock('vue-i18n', async importOriginal => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string, args?: unknown) => key + (args ? JSON.stringify(args) : '') }) }))
function catalog(id: number, models = ['first', 'shared']) {
  return { account_id: id, name: `Account ${id}`, platform: 'openai', type: 'apikey', is_cindy: false, models: models.map(id => ({ id, display_name: `Display ${id}` })) }
}
function mountModal(ids = [1, 2, 3]) {
 return mount(BatchTestAccountModal, { props: { show: true, accountIds: ids }, global: { stubs: {
  BaseDialog: { template: '<div><slot/><slot name="footer"/></div>' },
  AccountTestModelSelect: { props: ['modelValue', 'models', 'disabled', 'id'], emits: ['update:modelValue'], template: `<select :id="id" :value="modelValue" :disabled="disabled" @change="$emit('update:modelValue', $event.target.value)"><option v-for="m in models" :key="m.id" :value="m.id">{{ m.display_name }}</option></select>` }
 } } })
}
function button(wrapper: ReturnType<typeof mountModal>, text: string) { return wrapper.findAll('button').find(b => b.text().includes(text))! }
describe('BatchTestAccountModal per-account selections', () => {
 beforeEach(() => { vi.clearAllMocks(); batchTestModels.mockImplementation(async (ids: number[]) => ids.map(id => catalog(id))); batchTest.mockResolvedValue({ id: 12, status: 'pending' }) })
 it('keeps a fixed set, applies raw IDs only to supporting accounts and persists every choice', async () => {
  batchTestModels.mockResolvedValue([catalog(1), catalog(2, ['other']), catalog(3)])
  const wrapper = mountModal(); await flushPromises()
  await wrapper.setProps({ accountIds: [99] })
  await wrapper.get('#batch-model-1').setValue('shared')
  await button(wrapper, 'batchTest.apply').trigger('click')
  expect((wrapper.get('#batch-model-2').element as HTMLSelectElement).value).toBe('other')
  expect((wrapper.get('#batch-model-3').element as HTMLSelectElement).value).toBe('shared')
  expect(wrapper.text()).toContain('"applied":2,"skipped":1')
  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(batchTest).toHaveBeenCalledWith([{ account_id: 1, model_id: 'shared' }, { account_id: 2, model_id: 'other' }, { account_id: 3, model_id: 'shared' }], '')
  expect(wrapper.emitted('submitted')?.[0]).toEqual([{ id: 12, status: 'pending' }]); expect(wrapper.emitted('close')).toBeUndefined()
  wrapper.unmount()
 })
 it('blocks empty/error results until explicitly removed or successfully retried', async () => {
  batchTestModels.mockResolvedValueOnce([catalog(1), catalog(2, []), { ...catalog(3), error_code: 'catalog_failed' }])
  const wrapper = mountModal(); await flushPromises()
  expect(button(wrapper, 'batchTest.start').attributes('disabled')).toBeDefined()
  await wrapper.get('[data-account-id="2"] button').trigger('click')
  await button(wrapper, 'batchTest.retry').trigger('click'); await flushPromises()
  expect(batchTestModels.mock.calls[1][0]).toEqual([3])
  expect(button(wrapper, 'batchTest.start').attributes('disabled')).toBeUndefined()
  wrapper.unmount()
 })
 it('chunks discovery and pages at 100 rows with cross-page choices', async () => {
  const wrapper = mountModal(Array.from({ length: 101 }, (_, i) => i + 1)); await flushPromises()
  expect(batchTestModels.mock.calls.map(call => call[0].length)).toEqual([100, 1])
  expect(wrapper.findAll('[data-account-id]')).toHaveLength(100)
  await button(wrapper, 'batchTest.next').trigger('click')
  expect(wrapper.findAll('[data-account-id]')).toHaveLength(1)
  await wrapper.get('#batch-model-101').setValue('shared')
  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(batchTest.mock.calls[0][0]).toHaveLength(101)
  expect(batchTest.mock.calls[0][0][100]).toEqual({ account_id: 101, model_id: 'shared' })
  wrapper.unmount()
 })
 it('ignores canceled catalog results when reopened for a different selection', async () => {
  let resolve!: (value: unknown) => void
  batchTestModels.mockImplementationOnce(() => new Promise(r => { resolve = r }))
  const wrapper = mountModal([1]); await wrapper.setProps({ show: false }); await wrapper.setProps({ accountIds: [2], show: true }); await flushPromises()
  resolve([catalog(1)]); await flushPromises()
  expect(wrapper.find('[data-account-id="1"]').exists()).toBe(false); expect(wrapper.find('[data-account-id="2"]').exists()).toBe(true)
  expect(batchTestModels.mock.calls[0][1].aborted).toBe(true)
  wrapper.unmount()
 })
 it('blocks an off-page reasoning choice after applying another model without clearing the choice', async () => {
  batchTestModels.mockImplementation(async (ids: number[]) => ids.map(id => ({
   ...catalog(id),
   models: [
    { id: 'first', display_name: 'First', reasoning_efforts: ['high'] },
    { id: 'shared', display_name: 'Shared', reasoning_efforts: ['low'] },
   ],
  })))
  const wrapper = mountModal(Array.from({ length: 101 }, (_, index) => index + 1)); await flushPromises()
  await button(wrapper, 'batchTest.next').trigger('click')
  await wrapper.get('[data-account-id="101"] label.mt-3 select').setValue('high')
  await button(wrapper, 'batchTest.previous').trigger('click')
  await wrapper.get('#batch-model-1').setValue('shared')
  await button(wrapper, 'batchTest.apply').trigger('click')

  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(batchTest).toHaveBeenCalledTimes(0)
  expect(button(wrapper, 'batchTest.start').attributes('disabled')).toBeDefined()
  await button(wrapper, 'batchTest.next').trigger('click')
  expect(wrapper.getComponent(AccountTestReasoningSelect).props('modelValue')).toBe('high')
  await wrapper.get('[data-account-id="101"] label.mt-3 select').setValue('low')
  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(batchTest).toHaveBeenCalledTimes(1)
  expect(batchTest.mock.calls[0][0][100]).toEqual({ account_id: 101, model_id: 'shared', reasoning_effort: 'low' })
  wrapper.unmount()
 })
 it('keeps a nonempty reasoning choice when the model has no levels until an explicit default choice', async () => {
  batchTestModels.mockResolvedValue([{ ...catalog(1), models: [
   { id: 'first', display_name: 'First', reasoning_efforts: ['high'] },
   { id: 'plain', display_name: 'Plain' },
  ] }])
  const wrapper = mountModal([1]); await flushPromises()
  await wrapper.get('[data-account-id="1"] label.mt-3 select').setValue('high')
  await wrapper.get('#batch-model-1').setValue('plain')

  expect(wrapper.getComponent(AccountTestReasoningSelect).props('modelValue')).toBe('high')
  const reasoningSelect = wrapper.get('[data-account-id="1"] label.mt-3 select')
  expect((reasoningSelect.element as HTMLSelectElement).value).toBe('high')
  expect(reasoningSelect.get('option[value="high"]').attributes('disabled')).toBeDefined()
  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(batchTest).toHaveBeenCalledTimes(0)
  expect(button(wrapper, 'batchTest.start').attributes('disabled')).toBeDefined()

  await reasoningSelect.setValue('')
  expect(wrapper.getComponent(AccountTestReasoningSelect).props('modelValue')).toBe('')
  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(batchTest).toHaveBeenCalledTimes(1)
  expect(batchTest).toHaveBeenCalledWith([{ account_id: 1, model_id: 'plain' }], '')
  wrapper.unmount()
 })
})
