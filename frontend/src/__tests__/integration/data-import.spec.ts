import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import ImportDataModal from '@/components/admin/account/ImportDataModal.vue'
import AccountImportSettingsEditor, {
  type AccountImportSettingsDraft
} from '@/components/admin/account/AccountImportSettingsEditor.vue'
import type {
  AdminDataImportPreviewAccount,
  AdminDataImportPreviewResult,
  AdminDataImportResult
} from '@/types'

const showError = vi.fn()
const showSuccess = vi.fn()
const showWarning = vi.fn()

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess,
    showWarning
  })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      previewDataImport: vi.fn(),
      importData: vi.fn()
    }
  }
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key
  })
}))

const mountModal = () =>
  mount(ImportDataModal, {
    props: { show: true },
    global: {
      stubs: {
        BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
        Icon: true
      }
    }
  })

const makeJsonFile = (name: string, content: string, type = 'application/json') => {
  const file = new File([content], name, { type })
  Object.defineProperty(file, 'text', {
    value: () => Promise.resolve(content)
  })
  return file
}

const setInputFiles = (element: Element, files: File[]) => {
  Object.defineProperty(element, 'files', {
    value: files,
    configurable: true
  })
}

const dataFile = (name = 'data.json', accounts: Array<Record<string, unknown>> = [{ name: 'a' }]) =>
  makeJsonFile(name, JSON.stringify({
    type: 'sub2api-data',
    version: 2,
    exported_at: '2026-07-05T00:00:00Z',
    proxies: [],
    accounts
  }))

const previewAccount = (
  index: number,
  name: string,
  overrides: Partial<AdminDataImportPreviewAccount> = {}
): AdminDataImportPreviewAccount => ({
  index,
  name,
  platform: 'openai',
  type: 'oauth',
  valid: true,
  default_action: 'create',
  strong_identity_matches: [],
  ...overrides
})

const previewResult = (...accounts: AdminDataImportPreviewAccount[]): AdminDataImportPreviewResult => ({
  type: 'sub2api-data',
  version: 2,
  accounts,
  proxies: [],
  valid: true
})

const importResult = (overrides: Partial<AdminDataImportResult> = {}): AdminDataImportResult => ({
  proxy_created: 0,
  proxy_reused: 0,
  proxy_failed: 0,
  account_created: 1,
  account_updated: 0,
  account_skipped: 0,
  account_failed: 0,
  account_ids: [101],
  items: [{ index: 0, name: 'a', action: 'create', account_id: 101 }],
  ...overrides
})

const selectFiles = async (wrapper: VueWrapper, files: File[]) => {
  const input = wrapper.find('input[type="file"]')
  setInputFiles(input.element, files)
  await input.trigger('change')
}

const submitPreview = async (wrapper: VueWrapper) => {
  await wrapper.find('form').trigger('submit')
  await flushPromises()
}

describe('ImportDataModal', () => {
  beforeEach(async () => {
    showError.mockReset()
    showSuccess.mockReset()
    showWarning.mockReset()
    const { adminAPI } = await import('@/api/admin')
    vi.mocked(adminAPI.accounts.previewDataImport).mockReset()
    vi.mocked(adminAPI.accounts.importData).mockReset()
  })

  it('没有文件时禁用预览且不调用接口', () => {
    const wrapper = mountModal()

    expect(wrapper.get('[data-test="preview-import"]').attributes('disabled')).toBeDefined()
    expect(showError).not.toHaveBeenCalled()
  })

  it('无效 JSON 时按文件名提示解析失败且不请求预览', async () => {
    const { adminAPI } = await import('@/api/admin')
    const wrapper = mountModal()

    await selectFiles(wrapper, [makeJsonFile('data.json', 'invalid json')])
    await submitPreview(wrapper)

    expect(showError).toHaveBeenCalledWith('admin.accounts.dataImportParseFailedFile')
    expect(adminAPI.accounts.previewDataImport).not.toHaveBeenCalled()
    expect(adminAPI.accounts.importData).not.toHaveBeenCalled()
  })

  it('不是 Sub2 导出数据的 JSON 按文件名拒绝', async () => {
    const { adminAPI } = await import('@/api/admin')
    const wrapper = mountModal()

    await selectFiles(wrapper, [makeJsonFile('random.json', JSON.stringify({ name: 'test' }))])
    await submitPreview(wrapper)

    expect(showError).toHaveBeenCalledWith('admin.accounts.dataImportInvalidFile')
    expect(adminAPI.accounts.previewDataImport).not.toHaveBeenCalled()
    expect(adminAPI.accounts.importData).not.toHaveBeenCalled()
  })

  it('预览无写入，确认后才提交导入', async () => {
    const { adminAPI } = await import('@/api/admin')
    vi.mocked(adminAPI.accounts.previewDataImport).mockResolvedValue(previewResult(previewAccount(0, 'a')))
    vi.mocked(adminAPI.accounts.importData).mockResolvedValue(importResult())
    const wrapper = mountModal()

    await selectFiles(wrapper, [dataFile()])
    await submitPreview(wrapper)

    expect(adminAPI.accounts.previewDataImport).toHaveBeenCalledTimes(1)
    expect(adminAPI.accounts.importData).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="confirm-import"]').exists()).toBe(true)

    await wrapper.get('[data-test="confirm-import"]').trigger('click')
    await flushPromises()

    expect(adminAPI.accounts.importData).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('imported')).toEqual([[expect.objectContaining({ account_ids: [101] })]])
  })

  it('预览逐条显示代理复用和校验错误', async () => {
    const { adminAPI } = await import('@/api/admin')
    const preview = previewResult(previewAccount(0, 'a'))
    preview.proxies = [
      {
        index: 0,
        name: 'existing-proxy',
        protocol: 'http',
        valid: true,
        will_reuse: true,
        existing_proxy_id: 9
      },
      {
        index: 1,
        name: 'broken-proxy',
        protocol: 'socks5',
        valid: false,
        will_reuse: false,
        errors: ['proxy host is required']
      }
    ]
    vi.mocked(adminAPI.accounts.previewDataImport).mockResolvedValue(preview)
    const wrapper = mountModal()

    await selectFiles(wrapper, [dataFile()])
    await submitPreview(wrapper)

    const proxyPreview = wrapper.get('[data-test="import-proxy-preview"]')
    expect(proxyPreview.text()).toContain('admin.accounts.importProxyReuse')
    expect(proxyPreview.text()).toContain('admin.accounts.importProxyInvalid')
    expect(wrapper.get('[data-test="import-proxy-1"]').text()).toContain('proxy host is required')
    expect(adminAPI.accounts.importData).not.toHaveBeenCalled()
  })

  it('无有效 JSON 的后续选择不清空已有选择', async () => {
    const { adminAPI } = await import('@/api/admin')
    vi.mocked(adminAPI.accounts.previewDataImport).mockResolvedValue(previewResult(previewAccount(0, 'a')))
    const wrapper = mountModal()

    await selectFiles(wrapper, [dataFile('valid.json')])
    await selectFiles(wrapper, [new File(['hello'], 'notes.txt', { type: 'text/plain' })])
    expect(showError).toHaveBeenCalledWith('admin.accounts.dataImportSelectFile')

    await submitPreview(wrapper)

    expect(adminAPI.accounts.previewDataImport).toHaveBeenCalledWith(expect.objectContaining({
      accounts: [{ name: 'a' }]
    }))
    expect(adminAPI.accounts.importData).not.toHaveBeenCalled()
  })

  it('合并多个 JSON 后预览并按合并结果提交', async () => {
    const { adminAPI } = await import('@/api/admin')
    vi.mocked(adminAPI.accounts.previewDataImport).mockResolvedValue(previewResult(
      previewAccount(0, 'a'),
      previewAccount(1, 'b')
    ))
    vi.mocked(adminAPI.accounts.importData).mockResolvedValue(importResult({
      account_created: 2,
      account_ids: [101, 102],
      items: [
        { index: 0, name: 'a', action: 'create', account_id: 101 },
        { index: 1, name: 'b', action: 'create', account_id: 102 }
      ]
    }))
    const wrapper = mountModal()
    const first = dataFile('first.json', [{ name: 'a' }])
    const second = makeJsonFile('second.json', JSON.stringify({
      type: 'sub2api-data',
      version: 1,
      exported_at: '2026-07-05T00:00:01Z',
      proxies: [{ proxy_key: 'p' }],
      accounts: [{ name: 'b' }]
    }))

    await selectFiles(wrapper, [first, second])
    await submitPreview(wrapper)

    expect(adminAPI.accounts.previewDataImport).toHaveBeenCalledWith(expect.objectContaining({
      version: 2,
      proxies: [{ proxy_key: 'p' }],
      accounts: [{ name: 'a' }, { name: 'b' }]
    }))

    await wrapper.get('[data-test="confirm-import"]').trigger('click')
    await flushPromises()

    expect(adminAPI.accounts.importData).toHaveBeenCalledWith(expect.objectContaining({
      data: expect.objectContaining({
        proxies: [{ proxy_key: 'p' }],
        accounts: [{ name: 'a' }, { name: 'b' }]
      }),
      items: [
        { index: 0, action: 'create' },
        { index: 1, action: 'create' }
      ]
    }))
    expect(showSuccess).toHaveBeenCalledWith('admin.accounts.dataImportSuccess')
  })

  it('提交冲突动作、统一设置和逐账号覆盖', async () => {
    const { adminAPI } = await import('@/api/admin')
    vi.mocked(adminAPI.accounts.previewDataImport).mockResolvedValue(previewResult(
      previewAccount(0, 'conflict', {
        default_action: 'skip',
        strong_identity_matches: [{ account_id: 77, name: 'existing', matched_by: 'chatgpt_account_id' }]
      }),
      previewAccount(1, 'new')
    ))
    vi.mocked(adminAPI.accounts.importData).mockResolvedValue(importResult({
      account_created: 1,
      account_updated: 1,
      account_ids: [77, 102],
      items: [
        { index: 0, name: 'existing', action: 'update', account_id: 77 },
        { index: 1, name: 'renamed', action: 'create', account_id: 102 }
      ]
    }))
    const wrapper = mountModal()

    await selectFiles(wrapper, [dataFile('settings.json', [{ name: 'conflict' }, { name: 'new' }])])
    await submitPreview(wrapper)

    await wrapper.get('[data-test="import-action-0"]').setValue('update')
    await wrapper.get('[data-test="import-account-1"] button.icon-button').trigger('click')

    const editors = wrapper.findAllComponents(AccountImportSettingsEditor)
    const uniformEditor = editors.find(editor => editor.props('mode') === 'uniform')!
    const itemEditor = editors.find(editor => editor.props('mode') === 'item')!
    const uniform = JSON.parse(JSON.stringify(uniformEditor.props('modelValue'))) as AccountImportSettingsDraft
    uniform.enabled.namePrefix = true
    uniform.namePrefix = 'Batch-'
    uniform.enabled.proxy = true
    uniform.proxyID = '0'
    uniformEditor.vm.$emit('update:modelValue', uniform)
    const item = JSON.parse(JSON.stringify(itemEditor.props('modelValue'))) as AccountImportSettingsDraft
    item.enabled.name = true
    item.name = 'renamed'
    item.enabled.folder = true
    item.folder = 'Imported'
    item.enabled.tags = true
    item.tagsText = 'blue, urgent, blue'
    itemEditor.vm.$emit('update:modelValue', item)
    await flushPromises()

    await wrapper.get('[data-test="confirm-import"]').trigger('click')
    await flushPromises()

    expect(adminAPI.accounts.importData).toHaveBeenCalledWith(expect.objectContaining({
      skip_default_group_bind: true,
      uniform_settings: { name_prefix: 'Batch-', proxy_id: 0 },
      items: [
        { index: 0, action: 'update', existing_account_id: 77 },
        {
          index: 1,
          action: 'create',
          overrides: { name: 'renamed', management_folder: 'Imported', tags: ['blue', 'urgent'] }
        }
      ]
    }))
  })

  it('部分成功时立即通知父组件并进入结果页', async () => {
    const { adminAPI } = await import('@/api/admin')
    vi.mocked(adminAPI.accounts.previewDataImport).mockResolvedValue(previewResult(
      previewAccount(0, 'a'),
      previewAccount(1, 'b')
    ))
    const result = importResult({
      account_created: 1,
      account_failed: 1,
      account_ids: [101],
      items: [
        { index: 0, name: 'a', action: 'create', account_id: 101 },
        { index: 1, name: 'b', action: 'failed', error: 'failed' }
      ]
    })
    vi.mocked(adminAPI.accounts.importData).mockResolvedValue(result)
    const wrapper = mountModal()

    await selectFiles(wrapper, [dataFile('mixed.json', [{ name: 'a' }, { name: 'b' }])])
    await submitPreview(wrapper)
    await wrapper.get('[data-test="confirm-import"]').trigger('click')
    await flushPromises()

    expect(showWarning).toHaveBeenCalledWith('admin.accounts.dataImportCompletedWithErrors')
    expect(wrapper.emitted('imported')).toEqual([[result]])
    expect(wrapper.text()).toContain('admin.accounts.importSessionReady')
    expect(wrapper.emitted('close')).toBeUndefined()
  })
})
