import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import SystemPromptsView from '../SystemPromptsView.vue'

const mocks = vi.hoisted(() => ({
  list: vi.fn(), get: vi.fn(), create: vi.fn(), updateMetadata: vi.fn(), saveDraft: vi.fn(),
  syncManagedSource: vi.fn(), publish: vi.fn(), updateRuntime: vi.fn(), duplicate: vi.fn(), remove: vi.fn(),
  getSkillRegistry: vi.fn(), startSkillSync: vi.fn(), getSkillSync: vi.fn(), getSkillVersion: vi.fn(), publishSkillVersion: vi.fn(),
  copyToClipboard: vi.fn(), showSuccess: vi.fn(), showError: vi.fn(), showWarning: vi.fn(),
}))

vi.mock('@/api/admin/systemPrompts', () => ({ default: mocks }))
vi.mock('@/stores', () => ({ useAppStore: () => ({
  showSuccess: mocks.showSuccess, showError: mocks.showError, showWarning: mocks.showWarning,
}) }))
vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard: mocks.copyToClipboard }),
}))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ locale: { value: 'en' }, t: (key: string) => key }) }
})

const runtime = () => ({
  enabled: true, expose_server_prompt: false, compact_enabled: false,
  template_id: 1, version_id: 10, template_version: 1, revision: 5,
  sha256: 'a'.repeat(64), byte_length: 4, degraded: false,
  composition_mode: 'codex_skill_hybrid', bundle_id: 'codexrip-reverse-skill',
  bundle_manifest_sha256: '', registry_revision: 3, registry_manifest_sha256: '3'.repeat(64),
  registry_archive_sha256: '4'.repeat(64), registry_source_commit: '1'.repeat(40),
  bundle_available: true, bundle_degraded: false, updated_at: '2026-08-06T00:00:00Z',
})
const codexTemplate = () => ({
  id: 1, slug: 'codexrip_reverse_skill', name: 'CodexRip', description: 'hidden description',
  is_seed: true, managed_source: '', created_at: '2026-08-06T00:00:00Z', updated_at: '2026-08-06T00:00:00Z',
})
const gptTemplate = () => ({
  id: 2, slug: 'gpt_5_6_instruct', name: 'GPT-5.6 Instruct', description: 'hidden source description',
  is_seed: true, managed_source: 'mdx_tom_gpt_5_6_instruct', created_at: '2026-08-06T00:00:00Z', updated_at: '2026-08-06T00:00:00Z',
})
const version = (templateId = 1) => ({
  id: templateId === 1 ? 10 : 20, template_id: templateId, version: 1, body: 'seed', sha256: 'a'.repeat(64),
  byte_length: 4, note: 'seed', composition_mode: templateId === 1 ? 'codex_skill_hybrid' : 'inline',
  bundle_id: templateId === 1 ? 'codexrip-reverse-skill' : '', bundle_manifest_sha256: '',
  source_repository: templateId === 2 ? 'MDX-Tom/gpt-5.6-instruct' : '', source_commit: templateId === 2 ? '7'.repeat(40) : '',
  source_version: templateId === 2 ? 'v45' : '', source_artifact: templateId === 2 ? 'gpt-5.6-sol-unrestricted-v45.zip' : '',
  source_artifact_sha256: templateId === 2 ? 'c'.repeat(64) : '', source_license_sha256: templateId === 2 ? 'd'.repeat(64) : '',
  created_at: '2026-08-06T00:00:00Z', is_active: templateId === 1,
})
const skillRegistry = () => ({
  runtime: { revision: 3, active: { id: 30, bundle_id: 'codexrip-reverse-skill', source_commit: '1'.repeat(40), manifest_sha256: '3'.repeat(64), archive_sha256: '4'.repeat(64), file_count: 2, total_bytes: 30, added_files: 0, modified_files: 0, deleted_files: 0, script_changes: 0, binary_changes: 0, created_at: '2026-08-06T00:00:00Z' }, degraded: false, updated_at: '2026-08-06T00:00:00Z' },
  versions: [],
  client_install: {
    skill_name: 'codexrip-reverse-skill', descriptor_url: 'https://codexrip.vip/skills/reverse-skill/current.json',
    powershell: { strategy: 'verified_git_sparse_checkout', repository_url: 'https://github.com/HTExplicit/sub2api.git', repository_ref: 'v0.1.171-codexrip.7', repository_commit: '1'.repeat(40), bootstrap_path: 'deploy/skill-registry/bootstrap/powershell.ps1', bootstrap_sha256: '8'.repeat(64), acquire_command: 'acquire powershell', execute_command: 'execute powershell' },
    python: { strategy: 'verified_git_sparse_checkout', repository_url: 'https://github.com/HTExplicit/sub2api.git', repository_ref: 'v0.1.171-codexrip.7', repository_commit: '1'.repeat(40), bootstrap_path: 'deploy/skill-registry/bootstrap/python.py', bootstrap_sha256: '2'.repeat(64), acquire_command: 'acquire python', execute_command: 'execute python' },
  },
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
        BaseDialog: { template: '<div v-if="show"><slot /></div>', props: ['show'] },
        ConfirmDialog: ConfirmStub,
        Teleport: true,
      },
    },
  })
}

describe('SystemPromptsView', () => {
  beforeEach(() => {
    Object.values(mocks).forEach((mock) => mock.mockReset())
    mocks.list.mockResolvedValue({ templates: [codexTemplate(), gptTemplate()], runtime: runtime() })
    mocks.get.mockImplementation(async (id: number) => ({ template: id === 2 ? gptTemplate() : codexTemplate(), versions: [version(id)], runtime: runtime() }))
    mocks.getSkillRegistry.mockResolvedValue(skillRegistry())
    mocks.saveDraft.mockResolvedValue({ ...version(), id: 11, version: 2, body: 'draft', note: 'seed', is_active: false })
    mocks.publish.mockResolvedValue({ ...runtime(), version_id: 11, template_version: 2, revision: 6 })
  })

  it('keeps the main page focused on templates, editor, and history', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(mocks.getSkillRegistry).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="system-prompt-page-description"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="system-prompt-tab-preview"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="skill-registry-lifecycle"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('SHA-256')
    expect(wrapper.get('[data-test="system-prompt-tab-editor"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="system-prompt-tab-history"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="system-prompt-save-version"]').text()).toContain('saveVersion')
    expect(wrapper.get('[data-test="system-prompt-set-current"]').text()).toContain('setCurrent')
  })

  it('loads the advanced drawer lazily and closes it with Escape', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="system-prompt-open-advanced"]').trigger('click')
    await flushPromises()

    expect(mocks.getSkillRegistry).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-test="system-prompt-advanced-drawer"]').exists()).toBe(true)
    await wrapper.get('[data-test="system-prompt-advanced-drawer"]').trigger('keydown', { key: 'Escape' })
    expect(wrapper.find('[data-test="system-prompt-advanced-drawer"]').exists()).toBe(false)
  })

  it('loads the selected template from the mobile selector', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="system-prompt-mobile-template"]').setValue('2')
    await flushPromises()

    expect(mocks.get).toHaveBeenLastCalledWith(2)
    expect(wrapper.get('[data-test="system-prompt-body"]').element).toHaveProperty('value', 'seed')
  })

  it('keeps template creation reachable beside the mobile selector', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="system-prompt-mobile-create"]').trigger('click')

    expect(wrapper.text()).toContain('admin.systemPrompts.dialogs.slug')
  })

  it('saves a new version separately from setting it current', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="system-prompt-body"]').setValue('draft')
    await wrapper.get('[data-test="system-prompt-save-version"]').trigger('click')
    await flushPromises()

    expect(mocks.saveDraft).toHaveBeenCalledWith(1, expect.objectContaining({ body: 'draft', expected_latest_version: 1, expected_revision: 5 }))
    expect(mocks.publish).not.toHaveBeenCalled()
    await wrapper.get('[data-test="system-prompt-set-current"]').trigger('click')
    await wrapper.get('[data-test="confirm-action"]').trigger('click')
    await flushPromises()
    expect(mocks.publish).toHaveBeenCalledWith(1, 11, 5, false)
  })

  it('creates an inactive managed-source candidate without activating it', async () => {
    mocks.syncManagedSource.mockResolvedValue({ status: 'candidate_created', version: { ...version(2), id: 21, is_active: false, version: 2 } })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="system-prompt-template-2"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="system-prompt-open-advanced"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="system-prompt-source-sync"]').trigger('click')
    await flushPromises()

    expect(mocks.syncManagedSource).toHaveBeenCalledWith(2, { expected_latest_version: 1, expected_revision: 5 })
    expect(mocks.publish).not.toHaveBeenCalled()
    expect(wrapper.get('[data-test="system-prompt-source-candidate"]').exists()).toBe(true)
  })
})
