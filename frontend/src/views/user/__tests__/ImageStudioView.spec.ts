import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia, type Pinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ImageStudioView from '../ImageStudioView.vue'
import { useAuthStore } from '@/stores/auth'
import type { User } from '@/types'

const mocks = vi.hoisted(() => ({
  listEligibleKeys: vi.fn(),
  validateImage: vi.fn(),
  generate: vi.fn(),
  edit: vi.fn(),
  listHistory: vi.fn(),
  saveHistory: vi.fn(),
  deleteHistory: vi.fn(),
  clearHistory: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}))

vi.mock('@/api/imageStudio', () => ({
  MAX_IMAGE_BYTES: 20 * 1024 * 1024,
  listEligibleImageStudioKeys: mocks.listEligibleKeys,
  generateImages: mocks.generate,
  editImages: mocks.edit,
  validateImageBlob: mocks.validateImage,
}))
vi.mock('@/features/image-studio/history', () => ({
  listImageStudioHistory: mocks.listHistory,
  saveImageStudioHistory: mocks.saveHistory,
  deleteImageStudioHistory: mocks.deleteHistory,
  clearImageStudioHistory: mocks.clearHistory,
}))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: mocks.showError, showSuccess: mocks.showSuccess }),
}))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

describe('ImageStudioView', () => {
  let pinia: Pinia

  beforeEach(() => {
    vi.clearAllMocks()
    pinia = createPinia()
    setActivePinia(pinia)
    useAuthStore().$patch({ user: { id: 42 } as User })
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: vi.fn(() => 'blob:preview') })
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: vi.fn() })
    mocks.listEligibleKeys.mockResolvedValue({
      items: [{
        api_key: { id: 1, name: 'Images', key: 'sk-image', status: 'active', group_id: 10, group: { id: 10, name: 'Cindy', allow_image_generation: true } },
        capabilities: [
        {
          id: 'gpt-image-2',
          kind: 'image',
          input_modalities: ['text', 'image'],
          output_modalities: ['image'],
          endpoints: ['images.generations'],
          client_surfaces: ['image_studio'],
          controls: {
            generation: {
              sizes: ['1024x1024'],
              qualities: ['low'],
              max_output_count: 4,
            },
          },
        },
        {
          id: 'gemini-3-pro-image',
          kind: 'image',
          input_modalities: ['text', 'image'],
          output_modalities: ['image'],
          endpoints: ['images.generations', 'images.edits'],
          client_surfaces: ['image_studio'],
          controls: {
            generation: { sizes: ['1024x1024'], qualities: ['low'], max_output_count: 1 },
            edit: {
              sizes: ['1024x1024'],
              qualities: ['low'],
              max_output_count: 1,
              supports_reference_image: true,
              supports_mask: true,
            },
          },
        },
        { id: 'gpt-5.6-luna', kind: 'text', input_modalities: ['text'], output_modalities: ['text'], endpoints: ['responses'], client_surfaces: ['codex'] },
        ],
      }],
    })
    mocks.generate.mockImplementation((_apiKey: string, input: { n?: number }) => Promise.resolve(
      Array.from({ length: input.n || 1 }, (_, index) => ({
        blob: new Blob([`generated-${index}`], { type: 'image/png' }),
        mimeType: 'image/png',
      })),
    ))
    mocks.edit.mockResolvedValue([{ blob: new Blob(['edited'], { type: 'image/png' }), mimeType: 'image/png' }])
    mocks.listHistory.mockResolvedValue([])
    mocks.saveHistory.mockResolvedValue(true)
    mocks.deleteHistory.mockResolvedValue(true)
    mocks.clearHistory.mockResolvedValue(true)
    mocks.validateImage.mockResolvedValue('image/png')
  })

  function render() {
    return mount(ImageStudioView, {
      global: {
        plugins: [pinia],
        stubs: {
          AppLayout: { template: '<main><slot /></main>' },
          BaseDialog: { template: '<div v-if="show"><slot /></div>', props: ['show'] },
          Icon: { template: '<i />' },
        },
      },
    })
  }

  it('uses a responsive stable layout and only offers server-eligible keys', async () => {
    const wrapper = render()
    await flushPromises()

    expect(wrapper.get('[data-testid="image-studio-layout"]').classes()).toContain('lg:grid-cols-[minmax(320px,390px)_minmax(0,1fr)]')
    const options = wrapper.get('[data-testid="api-key-select"]').findAll('option').map(option => option.text())
    expect(options).toContain('Images · Cindy')
    expect(options.join(' ')).not.toContain('Text only')
  })

  it('loads eligible keys and their capabilities in one request', async () => {
    const defaultCapabilities = (await mocks.listEligibleKeys()).items[0].capabilities
    mocks.listEligibleKeys.mockResolvedValueOnce({
      items: [
        { api_key: { id: 1, name: 'First', key: 'sk-first', status: 'active', group_id: 10, group: { id: 10, name: 'Cindy' } }, capabilities: defaultCapabilities },
        { api_key: { id: 3, name: 'Second', key: 'sk-second', status: 'active', group_id: 10, group: { id: 10, name: 'Cindy' } }, capabilities: defaultCapabilities },
      ],
    })
    mocks.listEligibleKeys.mockClear()

    const wrapper = render()
    await flushPromises()

    expect(mocks.listEligibleKeys).toHaveBeenCalledTimes(1)
    const options = wrapper.get('[data-testid="api-key-select"]').findAll('option').map(option => option.text())
    expect(options).toContain('First · Cindy')
    expect(options).toContain('Second · Cindy')
  })

  it('loads capability-gated models and submits generation with the selected key', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="api-key-select"]').setValue('1')
    await flushPromises()

    expect(mocks.listEligibleKeys).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-testid="model-select"]').element.value).toBe('gpt-image-2')
    await wrapper.get('[data-testid="prompt-input"]').setValue('draw a lighthouse')
    await wrapper.get('#image-studio-count').setValue('4')
    await wrapper.get('[data-testid="submit-image"]').trigger('submit')
    await flushPromises()

    expect(mocks.generate).toHaveBeenCalledWith('sk-image', expect.objectContaining({
      model: 'gpt-image-2',
      prompt: 'draw a lighthouse',
      n: 4,
    }))
    expect(mocks.saveHistory).toHaveBeenCalledWith('user:42', expect.objectContaining({
      mode: 'generate',
      model: 'gpt-image-2',
      count: 4,
    }))
    const savedRecord = mocks.saveHistory.mock.calls[0]?.[1]
    expect(savedRecord).not.toHaveProperty('apiKey')
    expect(savedRecord).not.toHaveProperty('apiKeyId')
    expect(JSON.stringify(savedRecord)).not.toContain('sk-image')
  })

  it('does not save or render a completed request after the authenticated user changes', async () => {
    let finishGeneration: ((images: Array<{ blob: Blob; mimeType: string }>) => void) | undefined
    mocks.generate.mockReturnValueOnce(new Promise(resolve => {
      finishGeneration = resolve
    }))
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="api-key-select"]').setValue('1')
    await flushPromises()
    await wrapper.get('[data-testid="prompt-input"]').setValue('private prompt from user A')
    await wrapper.get('[data-testid="submit-image"]').trigger('submit')
    await flushPromises()

    useAuthStore().$patch({ user: { id: 84 } as User })
    await flushPromises()
    finishGeneration?.([{ blob: new Blob(['generated'], { type: 'image/png' }), mimeType: 'image/png' }])
    await flushPromises()

    expect(mocks.saveHistory).not.toHaveBeenCalled()
    expect(mocks.listHistory).toHaveBeenCalledWith('user:84')
    expect(wrapper.find('[data-testid="history-grid"]').exists()).toBe(false)
  })

  it('does not expose stale API keys when a previous user request resolves last', async () => {
    let finishUserA: ((value: unknown) => void) | undefined
    mocks.listEligibleKeys
      .mockReturnValueOnce(new Promise(resolve => {
        finishUserA = resolve
      }))
      .mockResolvedValueOnce({
        items: [{
          api_key: { id: 84, name: 'User B images', key: 'sk-user-b', status: 'active', group_id: 10, group: { id: 10, name: 'Cindy' } },
          capabilities: [{
            id: 'gpt-image-2', kind: 'image', input_modalities: ['text'], output_modalities: ['image'],
            endpoints: ['images.generations'], client_surfaces: ['image_studio'], controls: { generation: { max_output_count: 1 } },
          }],
        }],
      })

    const wrapper = render()
    await flushPromises()
    useAuthStore().$patch({ user: { id: 84 } as User })
    await flushPromises()

    finishUserA?.({
      items: [{ api_key: { id: 42, name: 'User A private key', key: 'sk-user-a', status: 'active', group_id: 10, group: { id: 10, name: 'Cindy' } }, capabilities: [] }],
    })
    await flushPromises()

    const keyOptions = wrapper.get('[data-testid="api-key-select"]').findAll('option').map(option => option.text())
    expect(keyOptions).toContain('User B images · Cindy')
    expect(keyOptions.join(' ')).not.toContain('User A private key')
    expect(mocks.listEligibleKeys).toHaveBeenCalledTimes(2)
  })

  it('clears prompt, uploads, and an open preview when the authenticated user changes', async () => {
    const userARecord = {
      id: 'user-a-history',
      createdAt: 123,
      mode: 'generate' as const,
      model: 'gpt-image-2',
      prompt: 'user A history',
      size: '1024x1024',
      quality: 'low',
      count: 1,
      images: [{ id: 'image-a', blob: new Blob(['image'], { type: 'image/png' }), mimeType: 'image/png' }],
    }
    mocks.listHistory.mockResolvedValueOnce([userARecord]).mockResolvedValueOnce([])
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="api-key-select"]').setValue('1')
    await flushPromises()
    await wrapper.get('[data-testid="model-select"]').setValue('gemini-3-pro-image')
    await wrapper.get('[data-testid="mode-edit"]').trigger('click')
    await wrapper.get('[data-testid="prompt-input"]').setValue('private prompt from user A')

    const reference = new File(['reference'], 'reference.png', { type: 'image/png' })
    const mask = new File(['mask'], 'mask.png', { type: 'image/png' })
    const referenceInput = wrapper.get('[data-testid="reference-upload"] input[type="file"]')
    const maskInput = wrapper.get('[data-testid="mask-upload"] input[type="file"]')
    Object.defineProperty(referenceInput.element, 'files', { configurable: true, value: [reference] })
    Object.defineProperty(maskInput.element, 'files', { configurable: true, value: [mask] })
    await referenceInput.trigger('change')
    await maskInput.trigger('change')
    await wrapper.get('[data-testid="history-grid"] .aspect-square > button').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="image-preview-dialog"]').exists()).toBe(true)

    useAuthStore().$patch({ user: { id: 84 } as User })
    await flushPromises()

    expect((wrapper.get('[data-testid="prompt-input"]').element as HTMLTextAreaElement).value).toBe('')
    expect(wrapper.find('[data-testid="reference-upload"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="mask-upload"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="image-preview-dialog"]').exists()).toBe(false)
    expect(URL.revokeObjectURL).toHaveBeenCalled()
  })

  it('keeps Gemini generation fixed to one output and hides a variable count control', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="api-key-select"]').setValue('1')
    await flushPromises()
    await wrapper.get('[data-testid="model-select"]').setValue('gemini-3-pro-image')

    expect(wrapper.find('#image-studio-count').exists()).toBe(false)
    await wrapper.get('[data-testid="prompt-input"]').setValue('draw a lake')
    await wrapper.get('[data-testid="submit-image"]').trigger('submit')
    await flushPromises()

    expect(mocks.generate).toHaveBeenCalledWith('sk-image', expect.objectContaining({
      model: 'gemini-3-pro-image',
      n: undefined,
      size: '1024x1024',
      quality: 'low',
    }))
  })

  it('uses multipart edit when a reference image is selected and retains an optional mask', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="api-key-select"]').setValue('1')
    await flushPromises()
    await wrapper.get('[data-testid="model-select"]').setValue('gemini-3-pro-image')
    await wrapper.get('[data-testid="mode-edit"]').trigger('click')
    await wrapper.get('[data-testid="prompt-input"]').setValue('replace the sky')

    const reference = new File(['reference'], 'reference.png', { type: 'image/png' })
    const mask = new File(['mask'], 'mask.png', { type: 'image/png' })
    const referenceInput = wrapper.get('[data-testid="reference-upload"] input[type="file"]')
    const maskInput = wrapper.get('[data-testid="mask-upload"] input[type="file"]')
    Object.defineProperty(referenceInput.element, 'files', { configurable: true, value: [reference] })
    Object.defineProperty(maskInput.element, 'files', { configurable: true, value: [mask] })
    await referenceInput.trigger('change')
    await maskInput.trigger('change')
    await flushPromises()
    await wrapper.get('[data-testid="submit-image"]').trigger('submit')
    await flushPromises()

    expect(mocks.edit).toHaveBeenCalledWith('sk-image', expect.objectContaining({
      model: 'gemini-3-pro-image',
      image: reference,
      mask,
      n: undefined,
      size: '1024x1024',
      quality: 'low',
    }))
  })

  it('keeps unverified gpt-image-2 edit controls unavailable', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="api-key-select"]').setValue('1')
    await flushPromises()

    expect(wrapper.get('[data-testid="model-select"]').element.value).toBe('gpt-image-2')
    expect(wrapper.get('[data-testid="mode-edit"]').attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-testid="reference-upload"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="mask-upload"]').exists()).toBe(false)
  })

  it('rejects disallowed or undecodable uploads before retaining them', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="api-key-select"]').setValue('1')
    await flushPromises()
    await wrapper.get('[data-testid="model-select"]').setValue('gemini-3-pro-image')
    await wrapper.get('[data-testid="mode-edit"]').trigger('click')

    const referenceInput = wrapper.get('[data-testid="reference-upload"] input[type="file"]')
    const svg = new File(['<svg/>'], 'reference.svg', { type: 'image/svg+xml' })
    Object.defineProperty(referenceInput.element, 'files', { configurable: true, value: [svg] })
    await referenceInput.trigger('change')
    await flushPromises()
    expect(mocks.validateImage).not.toHaveBeenCalled()
    expect(mocks.showError).toHaveBeenCalledWith('imageStudio.invalidFile')

    mocks.showError.mockClear()
    mocks.validateImage.mockRejectedValueOnce(new Error('decode failed'))
    const fakePng = new File(['not-png'], 'reference.png', { type: 'image/png' })
    Object.defineProperty(referenceInput.element, 'files', { configurable: true, value: [fakePng] })
    await referenceInput.trigger('change')
    await flushPromises()

    expect(mocks.validateImage).toHaveBeenCalledWith(fakePng, 'image/png')
    expect(mocks.showError).toHaveBeenCalledWith('imageStudio.invalidFile')
    expect(wrapper.get('[data-testid="submit-image"]').attributes('disabled')).toBeDefined()
  })

  it('shows a reference input without inventing a mask control', async () => {
    mocks.listEligibleKeys.mockResolvedValue({
      items: [{
        api_key: { id: 1, name: 'Edit only', key: 'sk-edit', status: 'active', group_id: 10, group: { id: 10, name: 'Cindy' } },
        capabilities: [{
          id: 'reference-only-image',
          kind: 'image',
          input_modalities: ['text', 'image'],
          output_modalities: ['image'],
          endpoints: ['images.edits'],
          client_surfaces: ['image_studio'],
          controls: {
            edit: {
              sizes: ['1024x1024'],
              qualities: ['low'],
              max_output_count: 1,
              supports_reference_image: true,
              supports_mask: false,
            },
          },
        }],
      }],
    })

    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="api-key-select"]').setValue('1')
    await flushPromises()

    expect(wrapper.find('[data-testid="reference-upload"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="mask-upload"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="mode-generate"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="mode-edit"]').attributes('disabled')).toBeUndefined()
  })

  it('recovers after a request error and leaves the form retryable', async () => {
    mocks.generate.mockRejectedValueOnce(new Error('temporary failure'))
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="api-key-select"]').setValue('1')
    await flushPromises()
    await wrapper.get('[data-testid="prompt-input"]').setValue('draw a bridge')
    await wrapper.get('[data-testid="submit-image"]').trigger('submit')
    await flushPromises()

    expect(mocks.showError).toHaveBeenCalledWith(expect.stringContaining('temporary failure'))
    expect(wrapper.get('[data-testid="submit-image"]').attributes('disabled')).toBeUndefined()
  })

  it('downloads and clears browser-only history', async () => {
    mocks.listHistory.mockResolvedValue([{
      id: 'run-1',
      createdAt: 123,
      mode: 'generate',
      model: 'gpt-image-2',
      prompt: 'draw history',
      size: '1024x1024',
      quality: 'low',
      count: 1,
      images: [{ id: 'image-1', blob: new Blob(['image'], { type: 'image/png' }), mimeType: 'image/png' }],
    }])
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    const wrapper = render()
    await flushPromises()

    const download = wrapper.findAll('button').find(button => button.attributes('title') === 'imageStudio.download')
    expect(download).toBeDefined()
    await download!.trigger('click')
    expect(clickSpy).toHaveBeenCalledTimes(1)

    await wrapper.get('[data-testid="clear-history"]').trigger('click')
    await flushPromises()
    expect(mocks.clearHistory).toHaveBeenCalledWith('user:42')
    expect(wrapper.find('[data-testid="empty-history"]').exists()).toBe(true)
  })

  it('keeps a record visible when its delete transaction fails to commit', async () => {
    mocks.listHistory.mockResolvedValue([{
      id: 'run-delete-failure',
      createdAt: 123,
      mode: 'generate',
      model: 'gpt-image-2',
      prompt: 'keep this record',
      size: '1024x1024',
      quality: 'low',
      count: 1,
      images: [{ id: 'image-1', blob: new Blob(['image'], { type: 'image/png' }), mimeType: 'image/png' }],
    }])
    mocks.deleteHistory.mockResolvedValue(false)
    const wrapper = render()
    await flushPromises()

    await wrapper.get('[data-testid="delete-history-run-delete-failure"]').trigger('click')
    await flushPromises()

    expect(mocks.deleteHistory).toHaveBeenCalledWith('user:42', 'run-delete-failure')
    expect(wrapper.find('[data-testid="delete-history-run-delete-failure"]').exists()).toBe(true)
    expect(mocks.showError).toHaveBeenCalledWith('imageStudio.historyUpdateFailed')
  })

  it('keeps all records visible when the clear transaction fails to commit', async () => {
    mocks.listHistory.mockResolvedValue([{
      id: 'run-clear-failure',
      createdAt: 123,
      mode: 'generate',
      model: 'gpt-image-2',
      prompt: 'keep all records',
      size: '1024x1024',
      quality: 'low',
      count: 1,
      images: [{ id: 'image-1', blob: new Blob(['image'], { type: 'image/png' }), mimeType: 'image/png' }],
    }])
    mocks.clearHistory.mockResolvedValue(false)
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    const wrapper = render()
    await flushPromises()

    await wrapper.get('[data-testid="clear-history"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-testid="delete-history-run-clear-failure"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="empty-history"]').exists()).toBe(false)
    expect(mocks.showError).toHaveBeenCalledWith('imageStudio.historyUpdateFailed')
  })

  it('does not clear the new user history when a previous owner clear resolves late', async () => {
    const record = (id: string, prompt: string) => ({
      id,
      createdAt: 123,
      mode: 'generate' as const,
      model: 'gpt-image-2',
      prompt,
      size: '1024x1024',
      quality: 'low',
      count: 1,
      images: [{ id: `image-${id}`, blob: new Blob(['image'], { type: 'image/png' }), mimeType: 'image/png' }],
    })
    mocks.listHistory
      .mockResolvedValueOnce([record('user-a', 'user A history')])
      .mockResolvedValueOnce([record('user-b', 'user B history')])
    let finishClear: ((cleared: boolean) => void) | undefined
    mocks.clearHistory.mockReturnValueOnce(new Promise(resolve => {
      finishClear = resolve
    }))
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    const wrapper = render()
    await flushPromises()

    await wrapper.get('[data-testid="clear-history"]').trigger('click')
    useAuthStore().$patch({ user: { id: 84 } as User })
    await flushPromises()
    expect(wrapper.find('[data-testid="delete-history-user-b"]').exists()).toBe(true)

    finishClear?.(true)
    await flushPromises()

    expect(mocks.clearHistory).toHaveBeenCalledWith('user:42')
    expect(wrapper.find('[data-testid="delete-history-user-b"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="empty-history"]').exists()).toBe(false)
  })

  it('retries a saved generation with the currently selected key', async () => {
    mocks.listHistory.mockResolvedValue([{
      id: 'run-retry',
      createdAt: 456,
      mode: 'generate',
      model: 'gpt-image-2',
      prompt: 'retry this image',
      size: '1024x1024',
      quality: 'low',
      count: 4,
      images: [{ id: 'image-retry', blob: new Blob(['image'], { type: 'image/png' }), mimeType: 'image/png' }],
    }])
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="api-key-select"]').setValue('1')
    await flushPromises()

    const retry = wrapper.findAll('button').find(button => button.attributes('title') === 'imageStudio.retry')
    expect(retry).toBeDefined()
    await retry!.trigger('click')
    await flushPromises()

    expect(mocks.generate).toHaveBeenCalledWith('sk-image', expect.objectContaining({
      model: 'gpt-image-2',
      prompt: 'retry this image',
      n: 4,
    }))
  })
})
