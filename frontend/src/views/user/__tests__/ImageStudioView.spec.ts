import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia, type Pinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ImageStudioView from '../ImageStudioView.vue'
import { useAuthStore } from '@/stores/auth'
import type { User } from '@/types'

const mocks = vi.hoisted(() => ({
  listEligibleKeys: vi.fn(),
  createJob: vi.fn(),
  listJobs: vi.fn(),
  waitJob: vi.fn(),
  cancelJob: vi.fn(),
  retryJob: vi.fn(),
  downloadArtifact: vi.fn(),
  validateImage: vi.fn(),
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
  createImageStudioJob: mocks.createJob,
  listImageStudioJobs: mocks.listJobs,
  waitForImageStudioJob: mocks.waitJob,
  cancelImageStudioJob: mocks.cancelJob,
  retryImageStudioJob: mocks.retryJob,
  downloadImageStudioArtifact: mocks.downloadArtifact,
  validateImageBlob: mocks.validateImage,
  isImageStudioJobTerminal: (status: string) => ['succeeded', 'partially_succeeded', 'failed', 'canceled', 'canceled_with_results'].includes(status),
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

const capabilities = [
  {
    object: 'model_capability',
    id: 'gpt-image-2',
    kind: 'image',
    input_modalities: ['text'],
    output_modalities: ['image'],
    endpoints: ['images.generations'],
    client_surfaces: ['image_studio'],
    controls: { generation: { sizes: ['1024x1024'], qualities: ['low'], max_output_count: 4 } },
  },
  {
    object: 'model_capability',
    id: 'gemini-3-pro-image',
    kind: 'image',
    input_modalities: ['text', 'image'],
    output_modalities: ['image'],
    endpoints: ['images.generations', 'images.edits'],
    client_surfaces: ['image_studio'],
    controls: {
      generation: { sizes: ['1024x1024'], qualities: ['low'], max_output_count: 4 },
      edit: { sizes: ['1024x1024'], qualities: ['low'], max_output_count: 4, supports_reference_image: true, supports_mask: true },
    },
  },
]

function pendingJob(input: Record<string, unknown> = {}) {
  return {
    id: 41,
    api_key_id: 1,
    mode: 'generate',
    model: 'gpt-image-2',
    count: 1,
    status: 'pending',
    counts: { processed: 0, succeeded: 0, failed: 0, canceled: 0 },
    ...input,
  }
}

function terminalDetail(input: Record<string, unknown> = {}) {
  return {
    job: pendingJob({ status: 'succeeded', counts: { processed: 1, succeeded: 1, failed: 0, canceled: 0 } }),
    items: [],
    artifacts: [{ id: 52, job_id: 41, kind: 'output', content_type: 'image/png', byte_size: 12, download_url: '/api/v1/image-studio/jobs/41/artifacts/52' }],
    ...input,
  }
}

describe('ImageStudioView job workflow', () => {
  let pinia: Pinia

  beforeEach(() => {
    vi.clearAllMocks()
    pinia = createPinia()
    setActivePinia(pinia)
    useAuthStore().$patch({ user: { id: 42 } as User })
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: vi.fn(() => 'blob:preview') })
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: vi.fn() })
    mocks.listEligibleKeys.mockResolvedValue({
      items: [{ api_key: { id: 1, name: 'Images', group_id: 10, group: { id: 10, name: 'Cindy' } }, capabilities }],
    })
    mocks.createJob.mockImplementation((input: Record<string, unknown>) => Promise.resolve(pendingJob({
      api_key_id: input.apiKeyId,
      mode: input.mode,
      model: input.model,
      count: input.count,
    })))
    mocks.listJobs.mockResolvedValue([])
    mocks.waitJob.mockResolvedValue(terminalDetail())
    mocks.cancelJob.mockResolvedValue(pendingJob({ status: 'running' }))
    mocks.retryJob.mockResolvedValue(pendingJob())
    mocks.downloadArtifact.mockResolvedValue(new Blob(['image'], { type: 'image/png' }))
    mocks.validateImage.mockResolvedValue('image/png')
    mocks.listHistory.mockResolvedValue([])
    mocks.saveHistory.mockResolvedValue(true)
    mocks.deleteHistory.mockResolvedValue(true)
    mocks.clearHistory.mockResolvedValue(true)
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

  async function selectDefaultModel(wrapper: ReturnType<typeof render>) {
    await flushPromises()
    await wrapper.get('[data-testid="api-key-select"]').setValue('1')
    await flushPromises()
  }

  it('keeps the existing responsive layout and displays secret-free eligible keys', async () => {
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('[data-testid="image-studio-layout"]').classes()).toContain('lg:grid-cols-[minmax(320px,390px)_minmax(0,1fr)]')
    expect(wrapper.get('[data-testid="api-key-select"]').text()).toContain('Images · Cindy')
    expect(JSON.stringify(mocks.listEligibleKeys.mock.results)).not.toContain('sk-')
  })

  it('shows the Studio output count control and submits a four-item generation job without a key secret', async () => {
    const wrapper = render()
    await selectDefaultModel(wrapper)
    await wrapper.get('[data-testid="prompt-input"]').setValue('draw a lighthouse')
    const countInput = wrapper.get('#image-studio-count')
    expect(countInput.attributes('min')).toBe('1')
    expect(countInput.attributes('max')).toBe('4')
    await countInput.setValue('4')
    mocks.waitJob.mockResolvedValueOnce(terminalDetail({
      job: pendingJob({ count: 4, status: 'succeeded', counts: { processed: 4, succeeded: 4, failed: 0, canceled: 0 } }),
      artifacts: Array.from({ length: 4 }, (_, index) => ({ id: 52 + index, job_id: 41, kind: 'output', content_type: 'image/png', byte_size: 12 })),
    }))

    await wrapper.get('[data-testid="submit-image"]').trigger('submit')
    await flushPromises()

    expect(mocks.createJob).toHaveBeenCalledWith(expect.objectContaining({ apiKeyId: 1, model: 'gpt-image-2', prompt: 'draw a lighthouse', count: 4 }))
    expect(JSON.stringify(mocks.createJob.mock.calls)).not.toContain('sk-')
    expect(mocks.waitJob).toHaveBeenCalledWith(41, expect.any(AbortSignal), 1000, expect.any(Function))
    expect(mocks.downloadArtifact).toHaveBeenCalledTimes(4)
    expect(mocks.saveHistory).toHaveBeenLastCalledWith('user:42', expect.objectContaining({ jobId: 41, status: 'succeeded', count: 4, images: expect.any(Array) }))
  })

  it('submits Gemini edits with local reference and optional mask', async () => {
    const wrapper = render()
    await selectDefaultModel(wrapper)
    await wrapper.get('[data-testid="model-select"]').setValue('gemini-3-pro-image')
    await wrapper.get('[data-testid="mode-edit"]').trigger('click')
    await wrapper.get('[data-testid="prompt-input"]').setValue('replace the sky')
    const countInput = wrapper.get('#image-studio-count')
    expect(countInput.attributes('max')).toBe('4')
    await countInput.setValue('4')
    const reference = new File(['reference'], 'reference.png', { type: 'image/png' })
    const mask = new File(['mask'], 'mask.png', { type: 'image/png' })
    const referenceInput = wrapper.get('[data-testid="reference-upload"] input[type="file"]')
    const maskInput = wrapper.get('[data-testid="mask-upload"] input[type="file"]')
    Object.defineProperty(referenceInput.element, 'files', { configurable: true, value: [reference] })
    Object.defineProperty(maskInput.element, 'files', { configurable: true, value: [mask] })
    await referenceInput.trigger('change')
    await maskInput.trigger('change')
    await wrapper.get('[data-testid="submit-image"]').trigger('submit')
    await flushPromises()

    expect(mocks.createJob).toHaveBeenCalledWith(expect.objectContaining({
      apiKeyId: 1, mode: 'edit', model: 'gemini-3-pro-image', count: 4, reference, mask,
    }))
  })

  it('shows progress and lets the session owner cancel without stopping in-flight polling', async () => {
    let finish: ((value: ReturnType<typeof terminalDetail>) => void) | undefined
    mocks.waitJob.mockImplementationOnce((_id, _signal, _delay, onProgress) => {
      onProgress(terminalDetail({ job: pendingJob({ status: 'running', count: 4, counts: { processed: 1, succeeded: 1, failed: 0, canceled: 0 } }) }))
      return new Promise(resolve => { finish = resolve })
    })
    const wrapper = render()
    await selectDefaultModel(wrapper)
    await wrapper.get('[data-testid="prompt-input"]').setValue('draw progress')
    await wrapper.get('[data-testid="submit-image"]').trigger('submit')
    await flushPromises()

    expect(wrapper.get('[data-testid="active-job-progress"]').text()).toContain('1/4')
    await wrapper.get('[data-testid="cancel-image-job"]').trigger('click')
    await flushPromises()
    expect(mocks.cancelJob).toHaveBeenCalledWith(41)

    finish?.(terminalDetail({ job: pendingJob({ status: 'canceled_with_results', counts: { processed: 1, succeeded: 1, failed: 0, canceled: 0 } }) }))
    await flushPromises()
  })

  it('persists partial terminal artifacts in owner-scoped IndexedDB history', async () => {
    mocks.waitJob.mockResolvedValueOnce(terminalDetail({
      job: pendingJob({ count: 2, status: 'partially_succeeded', counts: { processed: 2, succeeded: 1, failed: 1, canceled: 0 } }),
    }))
    const wrapper = render()
    await selectDefaultModel(wrapper)
    await wrapper.get('[data-testid="prompt-input"]').setValue('draw partial')
    await wrapper.get('[data-testid="submit-image"]').trigger('submit')
    await flushPromises()

    expect(mocks.saveHistory).toHaveBeenLastCalledWith('user:42', expect.objectContaining({
      jobId: 41,
      status: 'partially_succeeded',
      images: [expect.objectContaining({ id: 'artifact:52', mimeType: 'image/png' })],
    }))
    expect(mocks.showSuccess).toHaveBeenCalledWith('imageStudio.partialSaved')
  })

  it('aborts polling and ignores terminal artifacts after the session owner changes', async () => {
    let pollSignal: AbortSignal | undefined
    mocks.waitJob.mockImplementationOnce((_id, signal) => {
      pollSignal = signal
      return new Promise(() => {})
    })
    const wrapper = render()
    await selectDefaultModel(wrapper)
    await wrapper.get('[data-testid="prompt-input"]').setValue('private prompt')
    await wrapper.get('[data-testid="submit-image"]').trigger('submit')
    await flushPromises()

    useAuthStore().$patch({ user: { id: 84 } as User })
    await flushPromises()

    expect(pollSignal?.aborted).toBe(true)
    expect(mocks.downloadArtifact).not.toHaveBeenCalled()
    expect(mocks.listHistory).toHaveBeenCalledWith('user:84')
  })

  it('uses the server retry endpoint for a failed persisted job', async () => {
    mocks.listHistory.mockResolvedValue([{
      id: 'job:41', jobId: 41, status: 'failed', errorMessage: 'failed', createdAt: 123,
      mode: 'generate', model: 'gpt-image-2', prompt: 'retry this image', size: '1024x1024', quality: 'low', count: 1, images: [],
    }])
    const wrapper = render()
    await selectDefaultModel(wrapper)
    const retry = wrapper.findAll('button').find(button => button.attributes('title') === 'imageStudio.retry')
    expect(retry).toBeDefined()
    await retry!.trigger('click')
    await flushPromises()

    expect(mocks.retryJob).toHaveBeenCalledWith(41, expect.any(AbortSignal))
    expect(mocks.createJob).not.toHaveBeenCalled()
  })

  it('recovers after a create error and leaves the form retryable', async () => {
    mocks.createJob.mockRejectedValueOnce(new Error('temporary failure'))
    const wrapper = render()
    await selectDefaultModel(wrapper)
    await wrapper.get('[data-testid="prompt-input"]').setValue('draw a bridge')
    await wrapper.get('[data-testid="submit-image"]').trigger('submit')
    await flushPromises()

    expect(mocks.showError).toHaveBeenCalledWith(expect.stringContaining('temporary failure'))
    expect(wrapper.get('[data-testid="submit-image"]').attributes('disabled')).toBeUndefined()
  })
})
