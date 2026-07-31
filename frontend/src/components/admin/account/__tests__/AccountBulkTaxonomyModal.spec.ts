import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AccountBulkTaxonomyModal from '../AccountBulkTaxonomyModal.vue'

const { bulkUpdateTaxonomy, showError } = vi.hoisted(() => ({
  bulkUpdateTaxonomy: vi.fn(),
  showError: vi.fn()
}))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { bulkUpdateTaxonomy } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => `${key}${params?.count ?? ''}` }) }))

const BaseDialogStub = { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' }
const folders = [{ id: 3, name: 'Prod', sort_order: 0, account_count: 2, created_at: '', updated_at: '' }]
const tags = [{ id: 5, name: 'Paid', sort_order: 0, account_count: 1, created_at: '', updated_at: '' }]

describe('AccountBulkTaxonomyModal', () => {
  beforeEach(() => {
    bulkUpdateTaxonomy.mockReset().mockResolvedValue({ matched_count: 12, updated_count: 12 })
    showError.mockReset()
  })

  it('requires an effective change and sends a confirmed filtered target', async () => {
    const wrapper = mount(AccountBulkTaxonomyModal, {
      props: { show: true, target: { mode: 'filtered', filters: { statuses: 'active' }, count: 12 }, folders, tags },
      global: { stubs: { BaseDialog: BaseDialogStub } }
    })
    expect(wrapper.get('[data-test="bulk-taxonomy-submit"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-test="bulk-taxonomy-folder-set"]').setValue(true)
    await wrapper.get('[data-test="bulk-taxonomy-folder-select"]').setValue('3')
    await wrapper.get('[data-test="bulk-taxonomy-tag-add-5"]').setValue(true)
    expect(wrapper.get('[data-test="bulk-taxonomy-submit"]').attributes('disabled')).toBeUndefined()
    await wrapper.get('[data-test="bulk-taxonomy-submit"]').trigger('click')
    await flushPromises()
    expect(bulkUpdateTaxonomy).toHaveBeenCalledWith(expect.objectContaining({
      filters: { statuses: 'active' },
      expected_match_count: 12,
      folder_action: 'set',
      folder_id: 3,
      tag_add_ids: [5],
      tag_remove_ids: []
    }))
    expect(wrapper.emitted('updated')).toEqual([[12, 12]])
  })

  it('keeps tag add/remove mutually exclusive and reports count drift', async () => {
    bulkUpdateTaxonomy.mockRejectedValue({ response: { status: 409 } })
    const wrapper = mount(AccountBulkTaxonomyModal, {
      props: { show: true, target: { mode: 'selected', accountIds: [1, 2], count: 2 }, folders, tags },
      global: { stubs: { BaseDialog: BaseDialogStub } }
    })
    await wrapper.get('[data-test="bulk-taxonomy-tag-add-5"]').setValue(true)
    await wrapper.get('[data-test="bulk-taxonomy-tag-remove-5"]').setValue(true)
    await wrapper.get('[data-test="bulk-taxonomy-submit"]').trigger('click')
    await flushPromises()
    expect(bulkUpdateTaxonomy).toHaveBeenCalledWith(expect.objectContaining({ account_ids: [1, 2], tag_add_ids: [], tag_remove_ids: [5] }))
    expect(wrapper.emitted('stale')).toHaveLength(1)
    expect(showError).toHaveBeenCalled()
  })
})
