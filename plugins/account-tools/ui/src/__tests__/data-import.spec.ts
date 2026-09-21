import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import ImportDataModal from '../ImportDataModal.vue'
import { readAccountImportFiles } from '../accountImportWorker'
import AccountImportSettingsEditor, {
  type AccountImportSettingsDraft,
} from '../AccountImportSettingsEditor.vue'

const { importData, previewImportData, showError, showSuccess, showWarning } = vi.hoisted(() => ({
  importData: vi.fn(),
  previewImportData: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  showWarning: vi.fn(),
}))

vi.mock('../api', () => ({ adminAPI: { accounts: { importData, previewImportData } } }))
vi.mock('@sub2api/plugin-ui', async () => ({ ...await vi.importActual<typeof import('@sub2api/plugin-ui')>('@sub2api/plugin-ui'), useNotifications: () => ({ showError, showSuccess, showWarning, showInfo: vi.fn() }) }))
vi.mock('../accountImportWorker', async () => {
  const { parseAccountImportFiles } = await vi.importActual<typeof import('../accountImportParser')>('../accountImportParser')
  return { readAccountImportFiles: vi.fn((files: File[]) => parseAccountImportFiles(files)) }
})
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

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((finish) => { resolve = finish })
  return { promise, resolve }
}

describe('account data import job', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    importData.mockResolvedValue({ id: 81, kind: 'account_import', status: 'pending' })
    previewImportData.mockResolvedValue({ create_count: 2, update_count: 0, reject_count: 0, items: [] })
  })

  describe('draft preservation', () => {
    it('reports missing input without submitting', async () => {
      const wrapper = mountModal()

      await wrapper.get('#account-import-job-form').trigger('submit')

      expect(showError).toHaveBeenCalledTimes(1)
      expect(showError).toHaveBeenCalledWith('admin.accounts.dataImportSelectFile')
      expect(importData).not.toHaveBeenCalled()
      expect(previewImportData).not.toHaveBeenCalled()
      expect(wrapper.emitted('imported')).toBeUndefined()
    })

    it.each([
      { selection: 'non-JSON selection', rejected: true },
      { selection: 'picker cancellation', rejected: false },
    ])('keeps parsed draft after $selection', async ({ rejected }) => {
      const wrapper = mountModal()
      const keptFile = jsonFile('kept.json', payload('kept'))
      await selectFiles(wrapper, [keptFile])
      const editor = wrapper.getComponent(AccountImportSettingsEditor)
      const draft = JSON.parse(JSON.stringify(editor.props('modelValue'))) as AccountImportSettingsDraft
      draft.enabled.namePrefix = true
      draft.namePrefix = 'Draft-'
      editor.vm.$emit('update:modelValue', draft)
      await flushPromises()
      await wrapper.get('[data-test="preview-import"]').trigger('click')
      await flushPromises()
      const previousPreview = wrapper.get('[data-test="import-preview"]').html()

      await selectFiles(wrapper, rejected ? [new File(['notes'], 'notes.txt', { type: 'text/plain' })] : [])

      expect(wrapper.text()).toContain('kept.json')
      expect(wrapper.get('[data-test="import-preview"]').html()).toBe(previousPreview)
      expect(wrapper.get('[data-test="submit-import-job"]').attributes('disabled')).toBeUndefined()
      expect(readAccountImportFiles).toHaveBeenCalledTimes(1)
      expect(previewImportData).toHaveBeenCalledTimes(1)
      if (rejected) {
        expect(showError).toHaveBeenCalledTimes(1)
        expect(showError).toHaveBeenCalledWith('admin.accounts.dataImportSelectFile')
      } else expect(showError).not.toHaveBeenCalled()
      expect(showWarning).not.toHaveBeenCalled()

      await wrapper.get('#account-import-job-form').trigger('submit')
      await flushPromises()

      expect(importData).toHaveBeenCalledTimes(1)
      expect(importData).toHaveBeenCalledWith({
        data: payload('kept'),
        skip_default_group_bind: true,
        uniform_settings: { name_prefix: 'Draft-' },
      })
      expect(wrapper.emitted('imported')).toEqual([[{ id: 81, kind: 'account_import', status: 'pending' }]])
      expect(wrapper.emitted('close')).toBeUndefined()
      expect(showSuccess).not.toHaveBeenCalled()
    })

    it('keeps in-flight parsing after rejected and cancelled selections', async () => {
      const wrapper = mountModal()
      const contents = deferred<string>()
      const slowFile = new File([], 'slow.json', { type: 'application/json' })
      Object.defineProperty(slowFile, 'text', { value: () => contents.promise })
      await selectFiles(wrapper, [slowFile])
      const signal = vi.mocked(readAccountImportFiles).mock.calls[0]![1]

      await wrapper.get('#account-import-job-form').trigger('submit')
      expect(showError).not.toHaveBeenCalled()
      expect(importData).not.toHaveBeenCalled()
      expect(wrapper.get('[data-test="submit-import-job"]').attributes('disabled')).toBeDefined()
      await selectFiles(wrapper, [new File(['notes'], 'notes.txt', { type: 'text/plain' })])
      expect(signal.aborted).toBe(false)
      await selectFiles(wrapper, [])
      expect(signal.aborted).toBe(false)
      expect(readAccountImportFiles).toHaveBeenCalledTimes(1)
      expect(showError).toHaveBeenCalledTimes(1)
      expect(showError).toHaveBeenCalledWith('admin.accounts.dataImportSelectFile')

      contents.resolve(JSON.stringify(payload('slow')))
      await flushPromises()
      expect(wrapper.text()).toContain('slow.json')
      expect(wrapper.get('[data-test="submit-import-job"]').attributes('disabled')).toBeUndefined()
      await wrapper.get('#account-import-job-form').trigger('submit')
      await flushPromises()
      expect(importData).toHaveBeenCalledTimes(1)
      expect(importData).toHaveBeenCalledWith({
        data: payload('slow'), skip_default_group_bind: true, uniform_settings: {},
      })
    })

    it('keeps in-flight preview after a rejected selection', async () => {
      const wrapper = mountModal()
      await selectFiles(wrapper, [jsonFile('preview.json', payload('preview'))])
      const pendingPreview = deferred<unknown>()
      previewImportData.mockReturnValueOnce(pendingPreview.promise)
      await wrapper.get('[data-test="preview-import"]').trigger('click')

      await selectFiles(wrapper, [new File(['notes'], 'notes.txt', { type: 'text/plain' })])
      expect(wrapper.get('[data-test="preview-import"]').text()).toBe('admin.accounts.dataImportPreviewing')
      pendingPreview.resolve({
        create_count: 1, update_count: 0, reject_count: 0,
        items: [{ index: 0, action: 'create', message: 'preview completed' }],
      })
      await flushPromises()

      expect(wrapper.get('[data-test="import-preview"]').text()).toContain('preview completed')
      expect(wrapper.text()).toContain('preview.json')
      expect(previewImportData).toHaveBeenCalledTimes(1)
      expect(importData).not.toHaveBeenCalled()
    })

    it('replaces the draft for accepted mixed selections and keeps the warning', async () => {
      const wrapper = mountModal()
      await selectFiles(wrapper, [jsonFile('old.json', payload('old'))])
      await wrapper.get('[data-test="preview-import"]').trigger('click')
      await flushPromises()
      const nextFile = jsonFile('next.json', payload('next'))

      await selectFiles(wrapper, [nextFile, new File(['notes'], 'notes.txt', { type: 'text/plain' })])

      expect(showWarning).toHaveBeenCalledTimes(1)
      expect(showWarning).toHaveBeenCalledWith('admin.accounts.dataImportIgnoredFiles')
      expect(showError).not.toHaveBeenCalled()
      expect(vi.mocked(readAccountImportFiles).mock.calls[1]![0]).toEqual([nextFile])
      expect(wrapper.text()).toContain('next.json')
      expect(wrapper.text()).not.toContain('old.json')
      expect(wrapper.find('[data-test="import-preview"]').exists()).toBe(false)
      await wrapper.get('#account-import-job-form').trigger('submit')
      await flushPromises()
      expect(importData).toHaveBeenCalledTimes(1)
      expect(importData).toHaveBeenCalledWith({
        data: payload('next'), skip_default_group_bind: true, uniform_settings: {},
      })
    })

    it('does not fall back to an old payload when accepted new JSON fails parsing', async () => {
      const wrapper = mountModal()
      await selectFiles(wrapper, [jsonFile('old.json', payload('old'))])
      await wrapper.get('[data-test="preview-import"]').trigger('click')
      await flushPromises()

      await selectFiles(wrapper, [jsonFile('broken.json', '{')])

      expect(showError).toHaveBeenCalledTimes(1)
      expect(showError).toHaveBeenCalledWith('admin.accounts.dataImportParseFailedFile')
      expect(wrapper.find('[data-test="import-preview"]').exists()).toBe(false)
      expect(wrapper.get('[data-test="submit-import-job"]').attributes('disabled')).toBeDefined()
      await wrapper.get('#account-import-job-form').trigger('submit')
      expect(importData).not.toHaveBeenCalled()
      expect(wrapper.emitted('imported')).toBeUndefined()
    })

    it('keeps the busy gate and emits only the returned asynchronous job', async () => {
      const wrapper = mountModal()
      await selectFiles(wrapper, [jsonFile('busy.json', payload('busy'))])
      const pendingJob = deferred<unknown>()
      importData.mockReturnValueOnce(pendingJob.promise)

      await wrapper.get('#account-import-job-form').trigger('submit')
      await wrapper.get('#account-import-job-form').trigger('submit')
      await selectFiles(wrapper, [new File(['notes'], 'notes.txt', { type: 'text/plain' })])

      expect(importData).toHaveBeenCalledTimes(1)
      expect(readAccountImportFiles).toHaveBeenCalledTimes(1)
      expect(showError).not.toHaveBeenCalled()
      expect(wrapper.text()).toContain('busy.json')
      expect(wrapper.emitted('imported')).toBeUndefined()
      const job = { id: 93, kind: 'account_import', status: 'pending' }
      pendingJob.resolve(job)
      await flushPromises()
      expect(wrapper.emitted('imported')).toEqual([[job]])
      expect(wrapper.emitted('close')).toBeUndefined()
      expect(showSuccess).not.toHaveBeenCalled()
    })

    it('keeps the missing-proxy gate distinct from missing input', async () => {
      const wrapper = mountModal([{ id: 7, name: 'Managed Proxy' }])
      await selectFiles(wrapper, [jsonFile('proxy.json', payload('proxy'))])
      await wrapper.get('input[type="radio"][value="existing"]').setValue(true)

      await wrapper.get('#account-import-job-form').trigger('submit')

      expect(showError).toHaveBeenCalledTimes(1)
      expect(showError).toHaveBeenCalledWith('admin.accounts.importProxyRequired')
      expect(importData).not.toHaveBeenCalled()
      expect(wrapper.get('[data-test="submit-import-job"]').attributes('disabled')).toBeDefined()
    })
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
