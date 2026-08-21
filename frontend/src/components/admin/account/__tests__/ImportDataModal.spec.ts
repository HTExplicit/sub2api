import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const { importData, showError } = vi.hoisted(() => ({
  importData: vi.fn(),
  showError: vi.fn(),
}))

vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { importData } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError, showWarning: vi.fn() }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

import ImportDataModal from '../ImportDataModal.vue'

const BaseDialogStub = {
  props: ['show'],
  template: '<div v-if="show"><slot/><slot name="footer"/></div>',
}

describe('ImportDataModal', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    importData.mockResolvedValue({ id: 71, kind: 'account_import', status: 'pending' })
  })

  it('parses JSON locally and submits one import job without a preview stage', async () => {
    const wrapper = mount(ImportDataModal, {
      props: { show: true },
      global: {
        stubs: {
          BaseDialog: BaseDialogStub,
          Icon: true,
          AccountImportSettingsEditor: true,
        },
      },
    })
    const payload = {
      type: 'sub2api-data',
      version: 2,
      exported_at: '2026-08-21T00:00:00Z',
      proxies: [],
      accounts: [{ name: 'Cindy A', platform: 'cindy', type: 'apikey' }],
    }
    const file = {
      name: 'accounts.json',
      type: 'application/json',
      text: vi.fn().mockResolvedValue(JSON.stringify(payload)),
    }
    const input = wrapper.get('input[type="file"]')
    Object.defineProperty(input.element, 'files', { configurable: true, value: [file] })
    await input.trigger('change')
    await flushPromises()

    expect(wrapper.find('[data-test="preview-import"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('1')
    await wrapper.get('#account-import-job-form').trigger('submit')
    await flushPromises()

    expect(importData).toHaveBeenCalledWith({
      data: payload,
      skip_default_group_bind: true,
      uniform_settings: {},
    })
    expect(wrapper.emitted('imported')?.[0]?.[0]).toMatchObject({ id: 71, status: 'pending' })
    expect(wrapper.emitted('close')).toHaveLength(1)
  })
})
