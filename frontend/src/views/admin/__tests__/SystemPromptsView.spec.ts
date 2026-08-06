import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import SystemPromptsView from '../SystemPromptsView.vue'

const mocks = vi.hoisted(() => ({
  list: vi.fn(), get: vi.fn(), create: vi.fn(), updateMetadata: vi.fn(), saveDraft: vi.fn(),
  publish: vi.fn(), updateRuntime: vi.fn(), duplicate: vi.fn(), remove: vi.fn(),
  previewMerge: vi.fn(), previewUpstream: vi.fn(),
  showSuccess: vi.fn(), showError: vi.fn(), showWarning: vi.fn(),
}))

vi.mock('@/api/admin/systemPrompts', () => ({ default: mocks }))
vi.mock('@/stores', () => ({ useAppStore: () => ({
  showSuccess: mocks.showSuccess, showError: mocks.showError, showWarning: mocks.showWarning,
}) }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ locale: { value: 'en' }, t: (key: string) => key }) }
})

const runtime = () => ({
  enabled: false, expose_server_prompt: false, compact_enabled: false,
  template_id: 1, version_id: 10, template_version: 1, revision: 5,
  sha256: 'a'.repeat(64), byte_length: 4, degraded: true, updated_at: '2026-08-06T00:00:00Z',
})
const template = () => ({
  id: 1, slug: 'seed', name: 'Seed', description: 'Description', is_seed: true,
  created_at: '2026-08-06T00:00:00Z', updated_at: '2026-08-06T00:00:00Z',
})
const version = () => ({
  id: 10, template_id: 1, version: 1, body: 'seed', sha256: 'a'.repeat(64),
  byte_length: 4, note: 'seed', created_at: '2026-08-06T00:00:00Z', is_active: true,
})

const ToggleStub = defineComponent({
  props: ['modelValue'], emits: ['update:modelValue'],
  template: '<button type="button" @click="$emit(\'update:modelValue\', !modelValue)">{{ modelValue }}</button>',
})
const ConfirmStub = defineComponent({
  props: ['show'], emits: ['confirm', 'cancel'],
  template: '<div v-if="show" data-test="confirm"><button data-test="confirm-action" @click="$emit(\'confirm\')">confirm</button></div>',
})

function mountView() {
  return mount(SystemPromptsView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        Icon: true,
        Toggle: ToggleStub,
        BaseDialog: { template: '<div><slot /></div>' },
        ConfirmDialog: ConfirmStub,
      },
    },
  })
}

describe('SystemPromptsView', () => {
  beforeEach(() => {
    Object.values(mocks).forEach((mock) => mock.mockReset())
    mocks.list.mockResolvedValue({ templates: [template()], runtime: runtime() })
    mocks.get.mockResolvedValue({ template: template(), versions: [version()], runtime: runtime() })
    mocks.saveDraft.mockResolvedValue({ ...version(), id: 11, version: 2, body: 'draft', note: 'seed', is_active: false })
    mocks.publish.mockResolvedValue({ ...runtime(), version_id: 11, template_version: 2, revision: 6, degraded: false })
  })

  it('shows degraded state and keeps draft save separate from publish', async () => {
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.find('[data-test="system-prompt-degraded"]').exists()).toBe(true)

    await wrapper.get('[data-test="system-prompt-body"]').setValue('draft')
    await wrapper.get('[data-test="system-prompt-save-draft"]').trigger('click')
    await flushPromises()
    expect(mocks.saveDraft).toHaveBeenCalledWith(1, expect.objectContaining({
      body: 'draft', expected_latest_version: 1, expected_revision: 5,
    }))
    expect(mocks.publish).not.toHaveBeenCalled()

    await wrapper.get('[data-test="system-prompt-publish-selected"]').trigger('click')
    await wrapper.get('[data-test="confirm-action"]').trigger('click')
    await flushPromises()
    expect(mocks.publish).toHaveBeenCalledWith(1, 11, 5, false)
  })

  it('shows stable CAS conflict feedback', async () => {
    mocks.updateMetadata.mockRejectedValue({ status: 409, reason: 'system_prompt_revision_conflict', message: 'conflict' })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="system-prompt-name"]').setValue('Changed')
    await wrapper.get('[data-test="system-prompt-save-metadata"]').trigger('click')
    await flushPromises()
    expect(mocks.updateMetadata).toHaveBeenCalledWith(1, expect.objectContaining({ expected_revision: 5 }))
    expect(wrapper.find('[data-test="system-prompt-conflict"]').exists()).toBe(true)
  })

  it('sanitizes markdown preview instead of executing arbitrary HTML', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="system-prompt-body"]').setValue('<img src=x onerror="alert(1)"><script>alert(2)</script>**safe**')
    await wrapper.get('[data-test="system-prompt-markdown-mode"]').trigger('click')
    await flushPromises()
    const preview = wrapper.get('[data-test="system-prompt-markdown-preview"]').html()
    expect(preview).toContain('<strong>safe</strong>')
    expect(preview).not.toContain('onerror')
    expect(preview).not.toContain('<script')
    expect(preview).not.toContain('<img')
  })
})
