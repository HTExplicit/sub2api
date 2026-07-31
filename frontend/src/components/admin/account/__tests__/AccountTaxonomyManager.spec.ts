import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AccountTaxonomyManager from '../AccountTaxonomyManager.vue'

const { reorderFolders, showError } = vi.hoisted(() => ({ reorderFolders: vi.fn(), showError: vi.fn() }))
vi.mock('@/api/admin', () => ({
  adminAPI: { accounts: {
    reorderFolders,
    reorderTags: vi.fn(),
    createFolder: vi.fn(), createTag: vi.fn(), updateFolder: vi.fn(), updateTag: vi.fn(), deleteFolder: vi.fn(), deleteTag: vi.fn()
  } }
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const BaseDialogStub = { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' }
const DraggableStub = {
  props: ['modelValue'],
  emits: ['start', 'update:modelValue', 'end'],
  template: '<div><button data-test="reverse-order" @click="$emit(\'start\'); $emit(\'update:modelValue\', [...modelValue].reverse()); $emit(\'end\')">reverse</button><slot /></div>'
}

describe('AccountTaxonomyManager', () => {
  it('restores the previous order when atomic persistence fails', async () => {
    reorderFolders.mockRejectedValueOnce(new Error('failed'))
    const wrapper = mount(AccountTaxonomyManager, {
      props: {
        show: true,
        folders: [
          { id: 1, name: 'A', sort_order: 0, account_count: 0, created_at: '', updated_at: '' },
          { id: 2, name: 'B', sort_order: 1, account_count: 0, created_at: '', updated_at: '' }
        ],
        tags: []
      },
      global: { stubs: { BaseDialog: BaseDialogStub, ConfirmDialog: true, VueDraggable: DraggableStub, Icon: true } }
    })
    const itemNames = () => wrapper.findAll('[data-test^="taxonomy-item-"]').map((item) => item.text()).join('|')
    expect(itemNames()).toContain('A')
    await wrapper.get('[data-test="reverse-order"]').trigger('click')
    await flushPromises()
    expect(reorderFolders).toHaveBeenCalledWith([2, 1])
    expect(wrapper.findAll('[data-test^="taxonomy-item-"]')[0].text()).toContain('A')
    expect(showError).toHaveBeenCalled()
  })
})
