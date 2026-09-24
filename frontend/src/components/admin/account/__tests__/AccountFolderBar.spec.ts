import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import AccountFolderBar from '../AccountFolderBar.vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('AccountFolderBar', () => {
  it('renders safe facet counts and never propagates NaN', async () => {
    const wrapper = mount(AccountFolderBar, {
      props: {
        folders: [{ id: 7, name: 'Production', sort_order: 0, account_count: Number.NaN, created_at: '', updated_at: '' }],
        activeFolder: '',
        total: Number.NaN,
        uncategorizedCount: Number.POSITIVE_INFINITY
      }
    })

    expect(wrapper.text()).not.toContain('NaN')
    expect(wrapper.text()).toContain('Production')
    expect(wrapper.find('aside').exists()).toBe(false)
    expect(wrapper.get('[data-test="desktop-taxonomy-bar"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="desktop-taxonomy-nav"]').classes()).toContain('overflow-x-auto')
    expect(wrapper.get('[data-test="desktop-taxonomy-manage"]').classes()).toContain('shrink-0')

    await wrapper.find('button[aria-haspopup="listbox"]').trigger('click')
    expect(wrapper.find('[role="listbox"]').exists()).toBe(true)
    expect(wrapper.findAll('[role="option"]').every((item) => item.classes().includes('min-h-11'))).toBe(true)
  })

  it('supports listbox arrow navigation and Escape on mobile', async () => {
    const wrapper = mount(AccountFolderBar, {
      attachTo: document.body,
      props: {
        folders: [{ id: 7, name: 'Production', sort_order: 0, account_count: 2, created_at: '', updated_at: '' }],
        activeFolder: '',
        total: 3,
        uncategorizedCount: 1
      }
    })
    const trigger = wrapper.get('button[aria-haspopup="listbox"]')
    trigger.element.focus()
    await trigger.trigger('keydown', { key: 'ArrowDown' })
    expect(wrapper.find('[role="listbox"]').exists()).toBe(true)
    expect((document.activeElement as HTMLElement).getAttribute('role')).toBe('option')
    await wrapper.get('[role="listbox"]').trigger('keydown', { key: 'ArrowDown' })
    expect((document.activeElement as HTMLElement).textContent).toContain('admin.accounts.folderUncategorized')
    await wrapper.get('[role="listbox"]').trigger('keydown', { key: 'Escape' })
    expect(wrapper.find('[role="listbox"]').exists()).toBe(false)
    expect(document.activeElement).toBe(trigger.element)
    wrapper.unmount()
  })

  it('shows an explicit retry state without inventing counts', async () => {
    const wrapper = mount(AccountFolderBar, {
      props: { folders: [], activeFolder: '', error: true }
    })
    const retry = wrapper.get('[data-test="desktop-taxonomy-retry"]')
    await retry.trigger('click')
    expect(wrapper.emitted('retry')).toHaveLength(1)
    expect(wrapper.text()).not.toContain('NaN')
  })
})
