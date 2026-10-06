import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import AccountBulkActionsBar from '../AccountBulkActionsBar.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key
  })
}))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess: vi.fn(), showError: vi.fn() })
}))
vi.mock('@/api/admin/systemPrompts', () => ({
  systemPromptsAPI: {
    get: vi.fn().mockResolvedValue({
      enabled: true,
      default_prompt_id: 'p-default',
      prompts: [{ id: 'p-default', name: 'Default', body: 'text', position: 'append', role: 'auto' }],
      usage: { inherit: 0, off: 0, custom: {} }
    })
  }
}))

// The selected accounts the list holds, one per platform given.
const loaded = (...platforms: string[]) => platforms.map(platform => ({ platform }))

describe('AccountBulkActionsBar', () => {
  it('allows selecting all results before any row is selected', async () => {
    const wrapper = mount(AccountBulkActionsBar, {
      props: {
        selectedIds: [],
        selectedAccounts: [],
        totalResults: 45,
        selectingAll: false,
        allResultsSelected: false
      }
    })

    const button = wrapper.findAll('button').find(item =>
      item.text().includes('admin.accounts.bulkActions.selectAllResults')
    )

    expect(button).toBeDefined()
    await button!.trigger('click')
    expect(wrapper.emitted('select-all-results')).toHaveLength(1)
  })

  it('preserves the upstream billing probe action from v0.1.166', async () => {
    const wrapper = mount(AccountBulkActionsBar, {
      props: {
        selectedIds: [1],
        selectedAccounts: loaded('openai'),
        totalResults: 45,
        selectingAll: false,
        allResultsSelected: false
      }
    })

    const button = wrapper.findAll('button').find(item =>
      item.text().includes('admin.accounts.bulkActions.probeUpstreamBilling')
    )

    expect(button).toBeDefined()
    await button!.trigger('click')
    expect(wrapper.emitted('probe-upstream-billing')).toHaveLength(1)
  })

  it('emits the account tier refresh action for selected accounts', async () => {
    const wrapper = mount(AccountBulkActionsBar, {
      props: {
        selectedIds: [1],
        selectedAccounts: loaded('openai'),
        totalResults: 1,
        selectingAll: false,
        allResultsSelected: false
      }
    })

    await wrapper.get('[data-test="refresh-tier"]').trigger('click')

    expect(wrapper.emitted('refresh-tier')).toHaveLength(1)
  })

  describe('system prompt binding', () => {
    const mountBar = (selectedIds: number[], selectedAccounts: ReturnType<typeof loaded>) => mount(AccountBulkActionsBar, {
      props: { selectedIds, selectedAccounts, totalResults: 45, selectingAll: false, allResultsSelected: false },
      global: { stubs: { BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' } } }
    })

    it('is offered unless every selected account is loaded and none of them takes a prompt', () => {
      const offered = (selectedIds: number[], selectedAccounts: ReturnType<typeof loaded>) =>
        mountBar(selectedIds, selectedAccounts).find('[data-test="account-prompt-binding-bulk"]').exists()

      // TypeSafe accounts only, one or several: the row menu has no entry for such an account either.
      expect(offered([1], loaded('typesafe'))).toBe(false)
      expect(offered([1, 2], loaded('typesafe', 'typesafe'))).toBe(false)
      expect(offered([1], loaded('openai'))).toBe(true)
      expect(offered([1, 2], loaded('typesafe', 'antigravity'))).toBe(true)
      // The selection names accounts the list has not loaded; the request skips those that take no prompt.
      expect(offered([1, 2], loaded('typesafe'))).toBe(true)
      expect(offered([1], [])).toBe(true)
    })

    it('says in its dialog which accounts are skipped, and what is in effect only for an account known to take a prompt', async () => {
      const dialog = async (selectedIds: number[], selectedAccounts: ReturnType<typeof loaded>) => {
        const wrapper = mountBar(selectedIds, selectedAccounts)
        await wrapper.get('[data-test="account-prompt-binding-bulk"]').trigger('click')
        await vi.dynamicImportSettled()
        await flushPromises()
        const scope = wrapper.find('[data-test="system-prompt-binding-scope"]')
        const shown = {
          scope: scope.exists() ? scope.text() : '',
          effect: wrapper.find('[data-test="system-prompt-binding-effect"]').exists()
        }
        wrapper.unmount()
        return shown
      }

      expect(await dialog([1], loaded('openai'))).toEqual({ scope: '', effect: true })
      expect(await dialog([1, 2], loaded('openai', 'grok')))
        .toEqual({ scope: 'admin.systemPrompts.binding.bulkHint', effect: true })
      expect(await dialog([1, 2], loaded('openai', 'typesafe')))
        .toEqual({ scope: 'admin.systemPrompts.binding.bulkSkipHint', effect: true })
      // One selected account the list has not loaded: it may be a TypeSafe account.
      expect(await dialog([1], [])).toEqual({ scope: 'admin.systemPrompts.binding.skipHint', effect: false })
      expect(await dialog([1, 2], loaded('typesafe')))
        .toEqual({ scope: 'admin.systemPrompts.binding.bulkSkipHint', effect: false })
    })
  })
})
