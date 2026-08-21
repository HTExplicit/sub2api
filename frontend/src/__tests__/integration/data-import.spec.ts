import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import ImportDataModal from '@/components/admin/account/ImportDataModal.vue'
import AccountImportSettingsEditor, {
  type AccountImportSettingsDraft,
} from '@/components/admin/account/AccountImportSettingsEditor.vue'

const { importData, showError, showSuccess, showWarning } = vi.hoisted(() => ({
  importData: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  showWarning: vi.fn(),
}))

vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { importData } } }))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showSuccess, showWarning }),
}))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

const mountModal = () => mount(ImportDataModal, {
  props: { show: true },
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
})
