import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import BatchTestAccountModal from '../BatchTestAccountModal.vue'
import type { BatchTestModelRow } from '@/api/admin/accountJobs'

const { batchTestModels, batchTest, post } = vi.hoisted(() => ({ batchTestModels: vi.fn(), batchTest: vi.fn(), post: vi.fn() }))
vi.mock('@/api/admin/accountJobs', () => ({ default: { batchTestModels, batchTest } }))
vi.mock('@/api/client', () => ({ apiClient: { post } }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { id: 1 } }) }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn() }) }))
vi.mock('@/components/admin/account-jobs/AccountOperationDialog.vue', () => ({ default: { props: ['job', 'show'], template: '<div><slot/><slot name="footer"/></div>' } }))
vi.mock('vue-i18n', async importOriginal => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))

function row(id: number, ids = [`raw-${id}-a`, `raw-${id}-b`], defaultID = ids[ids.length - 1] || ''): BatchTestModelRow {
  const models = ids.map(id => ({ id, type: 'model', display_name: `Label ${id}` }))
  return { account_id: id, name: `Account ${id}`, platform: 'openai', type: 'apikey', is_cindy: false,
    models: [{ id: 'not-in-plan', type: 'model', display_name: 'Do not infer from the raw legacy list' }],
    test_plan: { schema_version: 1, account_id: id, wire_platform: 'openai', default_mode: 'provided', models,
      mode_views: { provided: { model_ids: ids, default_model_id: defaultID }, connection: { model_ids: ids, default_model_id: defaultID } } } }
}
let wrapper: VueWrapper | undefined
function open(ids = [1, 2]) {
  wrapper = mount(BatchTestAccountModal, { props: { show: true, accountIds: ids }, global: { stubs: {
    AccountTestModelSelect: { props: ['modelValue', 'models', 'disabled', 'id'], emits: ['update:modelValue'], template: `<select :id="id" :value="modelValue" :disabled="disabled" @change="$emit('update:modelValue', $event.target.value)"><option v-for="m in models" :key="m.id" :value="m.id">{{ m.display_name }}</option></select>` }
  } } })
  for (const row of (wrapper.vm as any).rows) row.selection_mode = 'explicit'
  ;(wrapper.get('details').element as HTMLDetailsElement).open = true
  void wrapper.get('details').trigger('toggle')
  return wrapper
}
function start() { return wrapper!.findAll('button').find(button => button.text().includes('batchTest.start'))! }

beforeEach(() => {
  vi.clearAllMocks()
  batchTestModels.mockReset()
  batchTest.mockResolvedValue({ id: 12, metadata: {} })
})
afterEach(() => { wrapper?.unmount(); wrapper = undefined; vi.restoreAllMocks() })

describe('BatchTestAccountModal versioned plans', () => {
  it('uses each account plan, retains a valid explicit choice on refresh, and ignores unselected response accounts', async () => {
    batchTestModels.mockResolvedValueOnce([row(1), row(2, ['raw-2-c']), row(99)])
    const modal = open()
    await flushPromises()
    expect(modal.findAll('[data-account-id]')).toHaveLength(2)
    expect((modal.get('#batch-model-1').element as HTMLSelectElement).value).toBe('raw-1-b')
    expect((modal.get('#batch-model-2').element as HTMLSelectElement).value).toBe('raw-2-c')
    await modal.get('#batch-model-1').setValue('raw-1-a')
    batchTestModels.mockResolvedValueOnce([row(1)])
    await (modal.vm as any).load([1], (modal.vm as any).generation)
    await flushPromises()
    expect((modal.get('#batch-model-1').element as HTMLSelectElement).value).toBe('raw-1-a')
    expect(batchTest).not.toHaveBeenCalled()
    await modal.get('form').trigger('submit')
    await flushPromises()
    expect(batchTest).toHaveBeenCalledWith([{ account_id: 1, selection_mode: 'explicit', model_id: 'raw-1-a' }, { account_id: 2, selection_mode: 'explicit', model_id: 'raw-2-c' }], '')
    expect(batchTestModels.mock.calls.map(call => call[0])).toEqual([[1, 2], [1]])
  })

  it('blocks missing, mismatched, and failed plans without silently filtering the selected accounts', async () => {
    const missing = row(1)
    delete missing.test_plan
    const mismatched = row(2)
    mismatched.test_plan!.account_id = 99
    batchTestModels.mockResolvedValueOnce([missing, mismatched, { ...row(3), error_code: 'catalog_failed' }, row(99)])
    const modal = open([1, 2, 3])
    await flushPromises()
    expect(modal.findAll('[data-account-id]')).toHaveLength(3)
    expect(start().attributes('disabled')).toBeDefined()
    await modal.get('form').trigger('submit')
    expect(batchTest).not.toHaveBeenCalled()
    batchTestModels.mockResolvedValueOnce([row(2)])
    const retry = modal.get('[data-account-id="2"]').findAll('button').find(button => button.text().includes('batchTest.retry'))!
    await retry.trigger('click')
    await flushPromises()
    expect(batchTestModels.mock.calls[1][0]).toEqual([2])
    expect(start().attributes('disabled')).toBeDefined()
    expect(modal.findAll('[data-account-id]')).toHaveLength(3)
    await modal.get('[data-account-id="1"] button').trigger('click')
    await modal.get('[data-account-id="3"] button').trigger('click')
    await modal.get('form').trigger('submit')
    await flushPromises()
    expect(batchTest).toHaveBeenCalledWith([{ account_id: 2, selection_mode: 'explicit', model_id: 'raw-2-b' }], '')
  })

  it('requests the account test plan view for every row', async () => {
    const actual = await vi.importActual<typeof import('@/api/admin/accountJobs')>('@/api/admin/accountJobs')
    const controller = new AbortController()
    post.mockResolvedValueOnce({ data: { items: [row(7)] } })
    expect(await actual.default.batchTestModels([7], controller.signal)).toEqual([row(7)])
    expect(post).toHaveBeenCalledWith('/admin/accounts/batch-test-models', { account_ids: [7] }, { params: { view: 'account-test-plan-v1' }, signal: controller.signal })
    expect(batchTest).not.toHaveBeenCalled()
  })
})
