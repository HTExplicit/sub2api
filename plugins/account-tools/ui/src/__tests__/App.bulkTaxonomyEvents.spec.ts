import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import App from '../App.vue'

const { bulkUpdateTaxonomy, emitHostEvent, openHostJob, showError } = vi.hoisted(() => ({
  bulkUpdateTaxonomy: vi.fn(),
  emitHostEvent: vi.fn(),
  openHostJob: vi.fn(),
  showError: vi.fn(),
}))

vi.mock('../api', () => ({ adminAPI: { accounts: { bulkUpdateTaxonomy } } }))
vi.mock('@sub2api/plugin-ui', async () => ({
  ...await vi.importActual<typeof import('@sub2api/plugin-ui')>('@sub2api/plugin-ui'),
  usePluginContext: () => ({ value: {
    contribution_id: 'account-taxonomy-bulk',
    account_ids: [7, 11],
    view_props: {
      target: { mode: 'selected', accountIds: [7, 11], count: 2 },
      folders: [],
      tags: [],
    },
  } }),
  useNotifications: () => ({ showError }),
  emitHostEvent,
  openHostJob,
}))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('account-tools App bulk taxonomy bridge', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    bulkUpdateTaxonomy.mockResolvedValue({ id: 41, metadata: {} })
    emitHostEvent.mockResolvedValue(undefined)
    openHostJob.mockResolvedValue(undefined)
  })

  it('relays a real child stale event without widening scope or resubmitting', async () => {
    bulkUpdateTaxonomy.mockRejectedValueOnce({ status: 409, response: { status: 409 } })
    const wrapper = mount(App)
    await wrapper.get('[data-test="bulk-taxonomy-folder-clear"]').setValue(true)
    await wrapper.get('[data-test="bulk-taxonomy-submit"]').trigger('click')
    await flushPromises()

    expect(bulkUpdateTaxonomy).toHaveBeenCalledTimes(1)
    expect(bulkUpdateTaxonomy).toHaveBeenCalledWith({
      folder_action: 'clear', folder_id: undefined, tag_add_ids: [], tag_remove_ids: [], account_ids: [7, 11],
    })
    expect(showError).toHaveBeenCalledWith('admin.accounts.bulkTaxonomy.targetChanged')
    expect(emitHostEvent.mock.calls).toEqual([['stale', undefined]])
    expect(openHostJob).not.toHaveBeenCalled()

    await wrapper.get('footer button.btn-secondary').trigger('click')
    expect(emitHostEvent.mock.calls).toEqual([['stale', undefined], ['close', undefined]])
    expect(bulkUpdateTaxonomy).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('keeps successful updated events on the host-job path', async () => {
    const wrapper = mount(App)
    await wrapper.get('[data-test="bulk-taxonomy-folder-clear"]').setValue(true)
    await wrapper.get('[data-test="bulk-taxonomy-submit"]').trigger('click')
    await flushPromises()

    expect(bulkUpdateTaxonomy).toHaveBeenCalledTimes(1)
    expect(openHostJob).toHaveBeenCalledTimes(1)
    expect(openHostJob).toHaveBeenCalledWith(41)
    expect(emitHostEvent).not.toHaveBeenCalled()
    expect(showError).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
