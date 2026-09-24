import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AccountTaxonomyManager from '../AccountTaxonomyManager.vue'

const { reorderFolders, showError, deleteFolder, deleteTag } = vi.hoisted(() => ({ reorderFolders: vi.fn(), showError: vi.fn(), deleteFolder: vi.fn(), deleteTag: vi.fn() }))
vi.mock('@/api/admin', () => ({
  adminAPI: { accounts: {
    reorderFolders,
    reorderTags: vi.fn(),
    createFolder: vi.fn(), createTag: vi.fn(), updateFolder: vi.fn(), updateTag: vi.fn(), deleteFolder, deleteTag
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
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('deletes only the confirmed item of the active tab and keeps drafts', async () => {
    const wrapper = mount(AccountTaxonomyManager, {
      props: {
        show: true,
        folders: [{ id: 1, name: 'Folder', sort_order: 0, account_count: 2, created_at: '', updated_at: '' }],
        tags: [{ id: 9, name: 'Tag', sort_order: 0, account_count: 3, created_at: '', updated_at: '' }]
      },
      global: { stubs: { BaseDialog: BaseDialogStub, ConfirmDialog: true, VueDraggable: DraggableStub, Icon: true } }
    })
    await wrapper.get('input').setValue('retained draft')
    await wrapper.findAll('button').find(button => button.text() === 'admin.accounts.tags')!.trigger('click')
    await wrapper.get('[data-test="taxonomy-delete"]').trigger('click')
    wrapper.findComponent({ name: 'ConfirmDialog' }).vm.$emit('confirm')
    await flushPromises()
    expect(deleteTag).toHaveBeenCalledWith(9)
    expect(deleteFolder).not.toHaveBeenCalled()
    expect((wrapper.get('input').element as HTMLInputElement).value).toBe('retained draft')
    wrapper.unmount()
  })

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
