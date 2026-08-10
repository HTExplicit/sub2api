import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import SystemPromptsView from '../SystemPromptsView.vue'

const mocks = vi.hoisted(() => ({
  list: vi.fn(), get: vi.fn(), create: vi.fn(), updateMetadata: vi.fn(), saveDraft: vi.fn(),
  syncManagedSource: vi.fn(), publish: vi.fn(), updateRuntime: vi.fn(), duplicate: vi.fn(), remove: vi.fn(),
  getSkillRegistry: vi.fn(), startSkillSync: vi.fn(), getSkillSync: vi.fn(), getSkillVersion: vi.fn(), publishSkillVersion: vi.fn(),
  showSuccess: vi.fn(), showError: vi.fn(), showWarning: vi.fn(),
}))

vi.mock('@/api/admin/systemPrompts', () => ({ default: mocks }))
vi.mock('@/stores', () => ({ useAppStore: () => ({
  showSuccess: mocks.showSuccess, showError: mocks.showError, showWarning: mocks.showWarning,
}) }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  const { default: en } = await import('@/i18n/locales/en')
  const resolveMessage = (key: string) => key.split('.').reduce<unknown>((value, segment) => {
    if (!value || typeof value !== 'object') return undefined
    return (value as Record<string, unknown>)[segment]
  }, en)
  return { ...actual, useI18n: () => ({ locale: { value: 'en' }, t: (key: string) => resolveMessage(key) || key }) }
})

const runtime = () => ({
  enabled: true, expose_server_prompt: false, compact_enabled: false,
  template_id: 1, version_id: 10, template_version: 1, revision: 5,
  sha256: 'a'.repeat(64), byte_length: 4, degraded: false,
  composition_mode: 'codex_skill_hybrid', bundle_id: 'codexrip-reverse-skill',
  bundle_manifest_sha256: '', registry_revision: 3, registry_manifest_sha256: '3'.repeat(64),
  registry_archive_sha256: '4'.repeat(64), registry_source_commit: '1'.repeat(40),
  registry_source_id: 'github_official', registry_remote_root: 'https://raw.githubusercontent.com/example/skills',
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
const skillRegistry = () => {
  const active = { id: 30, bundle_id: 'codexrip-reverse-skill', source_id: 'github_official', remote_root: 'https://raw.githubusercontent.com/example/skills', source_commit: '1'.repeat(40), overlay_sha256: '2'.repeat(64), manifest_sha256: '3'.repeat(64), archive_sha256: '4'.repeat(64), file_count: 2, total_bytes: 30, added_files: 0, modified_files: 0, deleted_files: 0, script_changes: 0, binary_changes: 0, created_at: '2026-08-06T00:00:00Z' }
  const previous = { ...active, id: 29, source_id: 'moxinggang', remote_root: 'https://moxinggang.com/skills/security-research/current', source_commit: '9'.repeat(40) }
  return {
    runtime: { revision: 3, source_id: 'github_official', remote_root: 'https://raw.githubusercontent.com/example/skills', active, degraded: false, updated_at: '2026-08-06T00:00:00Z' },
    versions: [active, previous],
    client_install: {
      skill_name: 'codexrip-reverse-skill', source_id: 'github_official', remote_root: 'https://raw.githubusercontent.com/example/skills', descriptor_url: 'https://codexrip.vip/skills/reverse-skill/current.json',
      powershell: { strategy: 'verified_https_content_addressed', bootstrap_url: 'https://codexrip.vip/skills/bootstrap/2199e8c4e8a09278c9b79e17b05e5457308db0a7d593e0f933ad6bd0712845f9/bootstrap-reverse-skill.ps1', bootstrap_sha256: '2199e8c4e8a09278c9b79e17b05e5457308db0a7d593e0f933ad6bd0712845f9', acquire_command: 'Invoke-WebRequest https://codexrip.vip/skills/bootstrap/2199e8c4e8a09278c9b79e17b05e5457308db0a7d593e0f933ad6bd0712845f9/bootstrap-reverse-skill.ps1', execute_command: 'execute powershell' },
      python: { strategy: 'verified_https_content_addressed', bootstrap_url: 'https://codexrip.vip/skills/bootstrap/353878272c8972c00817cc7171d7a4a087b4203fa2758b7ba1d040ededde7dc9/bootstrap-reverse-skill.py', bootstrap_sha256: '353878272c8972c00817cc7171d7a4a087b4203fa2758b7ba1d040ededde7dc9', acquire_command: 'urllib https://codexrip.vip/skills/bootstrap/353878272c8972c00817cc7171d7a4a087b4203fa2758b7ba1d040ededde7dc9/bootstrap-reverse-skill.py', execute_command: 'execute python' },
    },
  }
}

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
    mocks.startSkillSync.mockResolvedValue({ id: 8, source_id: 'github_official', status: 'queued', progress_stage: 'queued', created_at: '2026-08-06T00:00:00Z' })
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
    expect(wrapper.text()).toContain('系统提示词')
    expect(wrapper.text()).toContain('CodexRip 逆向安全提示词')
    expect(wrapper.text()).not.toContain('Business System Prompts')
    expect(wrapper.text()).not.toContain('Templates')
    expect(wrapper.get('[data-test="system-prompt-save-version"]').text()).toContain('保存为新版本')
    expect(wrapper.get('[data-test="system-prompt-set-current"]').text()).toContain('设为当前')
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

  it('defaults source sync to GitHub and posts the selected source without publishing', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="system-prompt-open-advanced"]').trigger('click')
    await flushPromises()

    const drawer = wrapper.get('[data-test="system-prompt-advanced-drawer"]')
    const selector = drawer.get('[data-test="system-prompt-skill-source"]')
    expect((selector.element as HTMLSelectElement).value).toBe('github_official')
    expect(selector.findAll('option').map(option => option.text())).toEqual(['GitHub 官方', '模型港'])
    expect(drawer.text()).toContain('当前来源')
    expect(drawer.text()).toContain('GitHub 官方')
    expect(drawer.find('[data-test="system-prompt-copy-acquire"]').exists()).toBe(false)
    expect(drawer.find('[data-test="system-prompt-copy-execute"]').exists()).toBe(false)
    expect(drawer.text()).not.toContain('bootstrap-reverse-skill')
    expect(drawer.get('[title="回滚"]').exists()).toBe(true)

    await selector.setValue('moxinggang')
    await drawer.get('[data-test="system-prompt-skill-sync"]').trigger('click')
    await flushPromises()
    expect(mocks.startSkillSync).toHaveBeenCalledWith('moxinggang', 3)
    expect(mocks.publishSkillVersion).not.toHaveBeenCalled()
  })

  it('keeps a synced source candidate inactive until its publish action is explicit', async () => {
    const candidate = {
      ...skillRegistry().runtime.active,
      id: 31,
      source_id: 'moxinggang',
      remote_root: 'https://moxinggang.com/skills/security-research/current',
      source_commit: '5'.repeat(40),
      manifest_sha256: '6'.repeat(64),
      archive_sha256: '7'.repeat(64),
      verified: true,
    }
    mocks.startSkillSync.mockResolvedValue({ id: 9, source_id: 'moxinggang', status: 'queued', progress_stage: 'queued', created_at: '2026-08-06T00:00:00Z' })
    mocks.getSkillSync.mockResolvedValue({ id: 9, source_id: 'moxinggang', status: 'succeeded', progress_stage: 'verified', candidate_bundle_version_id: 31, created_at: '2026-08-06T00:00:00Z' })
    mocks.getSkillVersion.mockResolvedValue(candidate)
    mocks.publishSkillVersion.mockResolvedValue({ ...skillRegistry().runtime, source_id: 'moxinggang', active: candidate, revision: 4 })

    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="system-prompt-open-advanced"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="system-prompt-skill-source"]').setValue('moxinggang')

    vi.useFakeTimers()
    try {
      await wrapper.get('[data-test="system-prompt-skill-sync"]').trigger('click')
      await vi.advanceTimersByTimeAsync(1200)
      await flushPromises()

      const candidatePanel = wrapper.get('[data-test="system-prompt-skill-candidate"]')
      expect(candidatePanel.text()).toContain('候选来源 模型港')
      expect(candidatePanel.text()).toContain('清单哈希')
      expect(candidatePanel.text()).toContain('归档哈希')
      expect(mocks.publishSkillVersion).not.toHaveBeenCalled()

      await candidatePanel.get('[data-test="system-prompt-skill-publish-candidate"]').trigger('click')
      await flushPromises()
      expect(mocks.publishSkillVersion).toHaveBeenCalledWith(31, 3, false)
    } finally {
      vi.useRealTimers()
    }
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

    expect(wrapper.text()).toContain('标识')
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
