import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import SystemPromptsView from '../SystemPromptsView.vue'

const mocks = vi.hoisted(() => ({
  list: vi.fn(), get: vi.fn(), create: vi.fn(), updateMetadata: vi.fn(), saveDraft: vi.fn(),
  publish: vi.fn(), updateRuntime: vi.fn(), duplicate: vi.fn(), remove: vi.fn(),
  previewMerge: vi.fn(), previewUpstream: vi.fn(), listBundles: vi.fn(), getBundle: vi.fn(),
  getSkillRegistry: vi.fn(), startSkillSync: vi.fn(), getSkillSync: vi.fn(),
  getSkillVersion: vi.fn(), publishSkillVersion: vi.fn(),
  copyToClipboard: vi.fn(),
  showSuccess: vi.fn(), showError: vi.fn(), showWarning: vi.fn(),
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
  enabled: false, expose_server_prompt: false, compact_enabled: false,
  template_id: 1, version_id: 10, template_version: 1, revision: 5,
  sha256: 'a'.repeat(64), byte_length: 4, degraded: false,
  composition_mode: 'codex_skill_hybrid', bundle_id: 'codexrip-reverse-skill',
  bundle_manifest_sha256: '', registry_revision: 3, registry_manifest_sha256: '3'.repeat(64),
  registry_archive_sha256: '4'.repeat(64), registry_source_commit: '1'.repeat(40),
  bundle_available: true, bundle_degraded: false,
  updated_at: '2026-08-06T00:00:00Z',
})
const template = () => ({
  id: 1, slug: 'seed', name: 'Seed', description: 'Description', is_seed: true,
  created_at: '2026-08-06T00:00:00Z', updated_at: '2026-08-06T00:00:00Z',
})
const version = () => ({
  id: 10, template_id: 1, version: 1, body: 'seed', sha256: 'a'.repeat(64),
  byte_length: 4, note: 'seed', composition_mode: 'codex_skill_hybrid',
  bundle_id: 'codexrip-reverse-skill', bundle_manifest_sha256: '',
  created_at: '2026-08-06T00:00:00Z', is_active: true,
})

const skillVersion = () => ({
  id: 20, bundle_id: 'codexrip-reverse-skill', source_commit: '1'.repeat(40),
  overlay_sha256: '2'.repeat(64), manifest_sha256: '3'.repeat(64), archive_sha256: '4'.repeat(64),
  file_count: 538, total_bytes: 7948026, added_files: 0, modified_files: 0, deleted_files: 0,
  script_changes: 0, binary_changes: 0, created_at: '2026-08-06T00:00:00Z', published_at: '2026-08-06T00:00:00Z',
})

const skillRegistry = () => ({
  runtime: { revision: 3, active: skillVersion(), degraded: false, updated_at: '2026-08-06T00:00:00Z' },
  versions: [skillVersion()],
  client_install: {
    skill_name: 'codexrip-reverse-skill', source_commit: '1'.repeat(40), manifest_sha256: '3'.repeat(64),
    descriptor_url: 'https://codexrip.vip/skills/reverse-skill/current.json',
    powershell: { url: 'https://codexrip.vip/skills/bootstrap/powershell/bootstrap.ps1', sha256: '8'.repeat(64), command: 'pwsh verified-installer.ps1' },
    python: { url: 'https://codexrip.vip/skills/bootstrap/python/bootstrap.py', sha256: '2'.repeat(64), command: 'python3 verified-installer.py' },
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
    mocks.getSkillRegistry.mockResolvedValue(skillRegistry())
    mocks.saveDraft.mockResolvedValue({ ...version(), id: 11, version: 2, body: 'draft', note: 'seed', is_active: false })
    mocks.publish.mockResolvedValue({ ...runtime(), version_id: 11, template_version: 2, revision: 6, degraded: false })
  })

  it('keeps hybrid draft save separate from prompt publication and separates the skill lifecycle', async () => {
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[data-test="system-prompt-composition"]').text()).toContain('admin.systemPrompts.bundle.hybrid')
    expect(wrapper.get('[data-test="skill-registry-lifecycle"]').text()).toContain('01')
    expect(wrapper.get('[data-test="skill-registry-lifecycle"]').text()).toContain('02')
    expect(wrapper.get('[data-test="skill-registry-lifecycle"]').text()).toContain('03')
    expect(wrapper.get('[data-test="skill-client-install"]').text()).toContain('pwsh verified-installer.ps1')
    expect(wrapper.get('[data-test="skill-client-install"]').text()).toContain('8'.repeat(64))

    await wrapper.get('[data-test="system-prompt-body"]').setValue('draft')
    await wrapper.get('[data-test="system-prompt-save-draft"]').trigger('click')
    await flushPromises()
    expect(mocks.saveDraft).toHaveBeenCalledWith(1, expect.objectContaining({
      body: 'draft', expected_latest_version: 1, expected_revision: 5,
      composition_mode: 'codex_skill_hybrid', bundle_id: 'codexrip-reverse-skill', bundle_manifest_sha256: '',
    }))
    expect(mocks.publish).not.toHaveBeenCalled()

    await wrapper.get('[data-test="system-prompt-publish-selected"]').trigger('click')
    await wrapper.get('[data-test="confirm-action"]').trigger('click')
    await flushPromises()
    expect(mocks.publish).toHaveBeenCalledWith(1, 11, 5, false)
  })

  it('keeps historical offline bundle versions read-only and removes the selector', async () => {
    const legacyRuntime = { ...runtime(), degraded: true, composition_mode: 'offline_bundle', bundle_id: 'moxinggang-reverse-skill', bundle_manifest_sha256: 'b'.repeat(64), bundle_available: false }
    const legacyVersion = { ...version(), composition_mode: 'offline_bundle', bundle_id: 'moxinggang-reverse-skill', bundle_manifest_sha256: 'b'.repeat(64) }
    mocks.list.mockResolvedValue({ templates: [template()], runtime: legacyRuntime })
    mocks.get.mockResolvedValue({ template: template(), versions: [legacyVersion], runtime: legacyRuntime })
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-test="system-prompt-degraded"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="system-prompt-legacy-readonly"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="system-prompt-bundle-select"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="system-prompt-save-draft"]').attributes('disabled')).toBeDefined()
  })

  it('renders deterministic preview routing and effective prompt metadata', async () => {
    mocks.previewUpstream.mockResolvedValue({
      body: { instructions: 'compiled' },
      client_mode: 'openai_compatible',
      base_server_instructions: 'seed',
      final_server_instructions: 'compiled',
      application: {
        applied: true, carrier: 'instructions', revision: 5, sha256: 'a'.repeat(64),
        base_sha256: 'a'.repeat(64), effective_sha256: 'c'.repeat(64),
        bundle_id: 'codexrip-reverse-skill', bundle_manifest_sha256: '3'.repeat(64), bundle_revision: 3,
        route_ids: ['api-security'], document_ids: ['skills/api-security/SKILL.md'], omitted_document_ids: [],
        effective_byte_length: 12900, degraded: false,
      },
    })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="system-prompt-tab-preview"]').trigger('click')
    await wrapper.findAll('[data-test="system-prompt-client-mode"] button')[1].trigger('click')
    await wrapper.get('[data-test="system-prompt-preview-upstream"]').trigger('click')
    await flushPromises()

    expect(mocks.previewUpstream).toHaveBeenCalledWith(expect.objectContaining({
      template_id: 1, version_id: 10, composition_mode: 'codex_skill_hybrid',
      bundle_id: 'codexrip-reverse-skill', bundle_manifest_sha256: '', client_mode: 'openai_compatible',
    }))
    expect(wrapper.get('[data-test="system-prompt-preview-effective-hash"]').text()).toContain('c'.repeat(64))
    expect(wrapper.get('[data-test="system-prompt-preview-base"]').text()).toBe('seed')
    expect(wrapper.get('[data-test="system-prompt-preview-final"]').text()).toBe('compiled')
    expect(wrapper.get('[data-test="system-prompt-preview-routing"]').text()).toContain('api-security')
  })

  it('syncs to a verified candidate and requires explicit skill publication', async () => {
    vi.useFakeTimers()
    mocks.startSkillSync.mockResolvedValue({ id: 7, status: 'queued', progress_stage: 'queued', created_at: '2026-08-06T00:00:00Z' })
    mocks.getSkillSync.mockResolvedValue({ id: 7, status: 'succeeded', progress_stage: 'candidate_ready', candidate_bundle_version_id: 21, source_commit: '5'.repeat(40), created_at: '2026-08-06T00:00:00Z' })
    mocks.getSkillVersion.mockResolvedValue({ ...skillVersion(), id: 21, source_commit: '5'.repeat(40), manifest_sha256: '6'.repeat(64), verified: true, added_files: 2 })
    mocks.publishSkillVersion.mockResolvedValue({ revision: 4, active: { ...skillVersion(), id: 21 }, degraded: false, updated_at: '2026-08-06T00:00:00Z' })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-test="skill-registry-sync"]').trigger('click')
    expect(mocks.startSkillSync).toHaveBeenCalledWith(3)
    await vi.advanceTimersByTimeAsync(1200)
    await flushPromises()
    expect(wrapper.get('[data-test="skill-registry-candidate"]').text()).toContain('admin.systemPrompts.skillRegistry.verified')
    expect(mocks.publishSkillVersion).not.toHaveBeenCalled()

    await wrapper.get('[data-test="skill-registry-publish-candidate"]').trigger('click')
    await wrapper.get('[data-test="confirm-action"]').trigger('click')
    await flushPromises()
    expect(mocks.publishSkillVersion).toHaveBeenCalledWith(21, 3, false)
    vi.useRealTimers()
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
