import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import ImportDataModal from '@/components/admin/account/ImportDataModal.vue'
import AccountImportSettingsEditor, {
  type AccountImportSettingsDraft,
} from '@/components/admin/account/AccountImportSettingsEditor.vue'

const { importData, previewImportData, showError, showSuccess, showWarning } = vi.hoisted(() => ({
  importData: vi.fn(),
  previewImportData: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  showWarning: vi.fn(),
}))

vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { importData, previewImportData } } }))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showSuccess, showWarning, showInfo: vi.fn() }),
}))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

const mountModal = (proxies: any[] = []) => mount(ImportDataModal, {
  props: { show: true, proxies },
  global: {
    stubs: {
      BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
      Icon: true,
    },
  },
})

function jsonFile(name: string, value: unknown): File {
  const content = typeof value === 'string' ? value : JSON.stringify(value)
  const file = new File([content], name, { type: 'application/json' })
  Object.defineProperty(file, 'text', { value: () => Promise.resolve(content) })
  return file
}

async function selectFiles(wrapper: VueWrapper, files: File[]): Promise<void> {
  const input = wrapper.get('input[type="file"]')
  Object.defineProperty(input.element, 'files', { configurable: true, value: files })
  await input.trigger('change')
  await flushPromises()
}

function payload(name: string, version = 2, proxies: unknown[] = []) {
  return {
    type: 'sub2api-data',
    version,
    exported_at: '2026-08-21T00:00:00Z',
    proxies,
    accounts: [{ name }],
  }
}

describe('account data import job', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    importData.mockResolvedValue({ id: 81, kind: 'account_import', status: 'pending' })
    previewImportData.mockResolvedValue({ create_count: 2, update_count: 0, reject_count: 0, items: [] })
  })

  it('rejects invalid JSON locally without calling the import endpoint', async () => {
    const wrapper = mountModal()

    await selectFiles(wrapper, [jsonFile('broken.json', '{')])

    expect(showError).toHaveBeenCalledWith('admin.accounts.dataImportParseFailedFile')
    expect(importData).not.toHaveBeenCalled()
    expect(wrapper.get('[data-test="submit-import-job"]').attributes('disabled')).toBeDefined()
  })

  it('rejects JSON that is not a Sub2 data export', async () => {
    const wrapper = mountModal()

    await selectFiles(wrapper, [jsonFile('random.json', { name: 'not-an-export' })])

    expect(showError).toHaveBeenCalledWith('admin.accounts.dataImportInvalidFile')
    expect(importData).not.toHaveBeenCalled()
  })

  it('merges local files, applies uniform settings, and emits the submitted job', async () => {
    const wrapper = mountModal()
    await selectFiles(wrapper, [
      jsonFile('first.json', payload('first', 2)),
      jsonFile('second.json', payload('second', 1, [{ proxy_key: 'p1' }])),
    ])

    const editor = wrapper.getComponent(AccountImportSettingsEditor)
    const draft = JSON.parse(JSON.stringify(editor.props('modelValue'))) as AccountImportSettingsDraft
    draft.enabled.namePrefix = true
    draft.namePrefix = 'Batch-'
    editor.vm.$emit('update:modelValue', draft)
    await wrapper.get('[data-test="preview-import"]').trigger('click')
    await flushPromises()
    await wrapper.get('#account-import-job-form').trigger('submit')
    await flushPromises()

    expect(importData).toHaveBeenCalledWith({
      data: expect.objectContaining({
        version: 2,
        proxies: [{ proxy_key: 'p1' }],
        accounts: [{ name: 'first' }, { name: 'second' }],
      }),
      skip_default_group_bind: true,
      uniform_settings: { name_prefix: 'Batch-' },
    })
    expect(wrapper.emitted('imported')?.[0]?.[0]).toMatchObject({ id: 81, status: 'pending' })
    expect(showSuccess).not.toHaveBeenCalled()
  })

  it('applies one direct proxy strategy to the whole import', async () => {
    const wrapper = mountModal()
    await selectFiles(wrapper, [jsonFile('direct.json', payload('direct'))])

    await wrapper.get('input[type="radio"][value="direct"]').setValue(true)
    await wrapper.get('[data-test="preview-import"]').trigger('click')
    await flushPromises()
    await wrapper.get('#account-import-job-form').trigger('submit')
    await flushPromises()

    expect(previewImportData).toHaveBeenCalledWith(expect.objectContaining({
      uniform_settings: { proxy_id: 0 },
    }))
    expect(importData).toHaveBeenCalledWith(expect.objectContaining({
      uniform_settings: { proxy_id: 0 },
    }))
  })

  it('requires and applies one existing proxy for the whole import', async () => {
    const wrapper = mountModal([{ id: 7, name: 'Managed Proxy' }])
    await selectFiles(wrapper, [jsonFile('proxy.json', payload('proxied'))])

    await wrapper.get('input[type="radio"][value="existing"]').setValue(true)
    expect(wrapper.get('[data-test="preview-import"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-test="import-uniform-proxy"]').setValue('7')
    expect(wrapper.get('[data-test="preview-import"]').attributes('disabled')).toBeUndefined()
    await wrapper.get('[data-test="preview-import"]').trigger('click')
    await flushPromises()

    expect(previewImportData).toHaveBeenCalledWith(expect.objectContaining({
      uniform_settings: { proxy_id: 7 },
    }))
  })
})
