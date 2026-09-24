import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import SystemPromptsView from '../SystemPromptsView.vue'
import SystemPromptAdvancedDrawer from '@/components/admin/systemPrompt/SystemPromptAdvancedDrawer.vue'

const mocks = vi.hoisted(() => ({
  list: vi.fn(), get: vi.fn(), create: vi.fn(), updateMetadata: vi.fn(), saveDraft: vi.fn(),
  syncManagedSource: vi.fn(), publish: vi.fn(), updateRuntime: vi.fn(), duplicate: vi.fn(), remove: vi.fn(),
  getSkillRegistry: vi.fn(), startSkillSync: vi.fn(), getSkillSync: vi.fn(), getSkillVersion: vi.fn(), publishSkillVersion: vi.fn(),
  showSuccess: vi.fn(), showError: vi.fn(), showWarning: vi.fn(),
}))

vi.mock('@/api/admin/systemPrompts', () => ({ default: mocks }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({
  showSuccess: mocks.showSuccess, showError: mocks.showError, showWarning: mocks.showWarning,
}) }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  const { default: messages } = await import('@/i18n/locales/en/admin/systemPrompts')
  const en = { admin: messages, common: { confirm: 'Confirm', cancel: 'Cancel' } }
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
  bundle_manifest_sha256: '', registry_revision: 3, registry_raw_tree_sha256: '1'.repeat(64),
  registry_effective_tree_sha256: '2'.repeat(64), registry_prompt_raw_sha256: '3'.repeat(64),
  registry_prompt_effective_sha256: '4'.repeat(64), registry_upstream_source_id: 'moxinggang',
  registry_upstream_root: 'https://moxinggang.com/skills/security-research/current',
  registry_public_root: 'https://codexrip.vip/skills/security-research/current',
  bundle_available: true, bundle_degraded: false, updated_at: '2026-08-06T00:00:00Z',
})
const codexTemplate = () => ({
  id: 1, slug: 'codexrip_reverse_skill', name: 'CodexRip', description: 'hidden description',
  is_seed: true, managed_source: 'remote_skill_registry', created_at: '2026-08-06T00:00:00Z', updated_at: '2026-08-06T00:00:00Z',
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
  const activePrompt = { id: 40, raw_sha256: '3'.repeat(64), effective_sha256: '4'.repeat(64), diff: 'routing diff', created_at: '2026-08-11T00:00:00Z' }
  const active = { id: 30, upstream_source_id: 'moxinggang', upstream_root: 'https://moxinggang.com/skills/security-research/current', public_root: 'https://codexrip.vip/skills/security-research/current', raw_tree_sha256: '1'.repeat(64), effective_tree_sha256: '2'.repeat(64), prompt_version_id: 40, file_count: 458, raw_total_bytes: 3000, effective_total_bytes: 3100, added_files: 0, modified_files: 0, deleted_files: 0, script_changes: 0, binary_changes: 0, fetched_at: '2026-08-11T00:00:00Z', created_at: '2026-08-11T00:00:00Z' }
  const previous = { ...active, id: 29, effective_tree_sha256: '9'.repeat(64) }
  return {
    runtime: { revision: 3, active, active_prompt: activePrompt, degraded: false, updated_at: '2026-08-11T00:00:00Z' },
    versions: [active, previous],
    source: { upstream_source_id: 'moxinggang', upstream_root: 'https://moxinggang.com/skills/security-research/current', public_root: 'https://codexrip.vip/skills/security-research/current' },
  }
}
const skillVersionDetail = () => ({
  ...skillRegistry().runtime.active!,
  prompt: {
    ...skillRegistry().runtime.active_prompt!,
    raw_body: 'model-gang raw body',
    effective_body: 'server effective body',
  },
  file_changes: [],
  verified: true,
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
        ExtensionSurface: { template: '<div><slot /></div>' },
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

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail })
  return { promise, resolve, reject }
}

const thirdTemplate = () => ({ ...gptTemplate(), id: 3, slug: 'custom', name: 'Custom', managed_source: '' })
const templateDetail = (id: number) => ({
  template: id === 1 ? codexTemplate() : id === 2 ? gptTemplate() : thirdTemplate(),
  versions: [{ ...version(id), id: id * 10, body: id === 3 ? 'third body' : 'seed' }], runtime: runtime(),
})

describe('SystemPromptsView', () => {
  beforeEach(() => {
    Object.values(mocks).forEach((mock) => mock.mockReset())
    mocks.list.mockResolvedValue({ templates: [codexTemplate(), gptTemplate()], runtime: runtime() })
    mocks.get.mockImplementation(async (id: number) => ({ template: id === 2 ? gptTemplate() : codexTemplate(), versions: [version(id)], runtime: runtime() }))
    mocks.getSkillRegistry.mockResolvedValue(skillRegistry())
    mocks.getSkillVersion.mockResolvedValue(skillVersionDetail())
    mocks.startSkillSync.mockResolvedValue({ id: 8, status: 'queued', progress_stage: 'queued', prompt_capture_provided: false, created_at: '2026-08-11T00:00:00Z' })
    mocks.saveDraft.mockResolvedValue({ ...version(), id: 11, version: 2, body: 'draft', note: 'seed', is_active: false })
    mocks.publish.mockResolvedValue({ ...runtime(), version_id: 11, template_version: 2, revision: 6 })
  })

  it('shows the active effective managed body without legacy history', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(mocks.getSkillRegistry).toHaveBeenCalledTimes(1)
    expect(mocks.getSkillVersion).toHaveBeenCalledWith(30)
    expect(wrapper.find('[data-test="system-prompt-page-description"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="system-prompt-tab-preview"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="skill-registry-lifecycle"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('SHA-256')
    expect(wrapper.get('[data-test="system-prompt-tab-editor"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('System Prompts')
    expect(wrapper.text()).toContain('Security research remote Skill prompt')
    expect(wrapper.text()).not.toContain('Business System Prompts')
    expect(wrapper.text()).toContain('Templates')
    expect(wrapper.find('[data-test="system-prompt-tab-history"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="system-prompt-body"]').element).toHaveProperty('readOnly', true)
    expect(wrapper.get('[data-test="system-prompt-body"]').element).toHaveProperty('disabled', false)
    expect(wrapper.get('[data-test="system-prompt-body"]').element).toHaveProperty('value', 'server effective body')
    expect(wrapper.get('[data-test="system-prompt-body"]').element).not.toHaveProperty('value', 'seed')
    expect(wrapper.find('[data-test="system-prompt-save-version"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="system-prompt-set-current"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="system-prompt-edit-metadata"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="system-prompt-template-menu"]').exists()).toBe(false)
  })

  it('switches between effective and raw managed bodies', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-test="system-prompt-body"]').element).toHaveProperty('value', 'server effective body')
    await wrapper.get('[data-test="system-prompt-managed-raw"]').trigger('click')
    expect(wrapper.get('[data-test="system-prompt-body"]').element).toHaveProperty('value', 'model-gang raw body')
    await wrapper.get('[data-test="system-prompt-managed-effective"]').trigger('click')
    expect(wrapper.get('[data-test="system-prompt-body"]').element).toHaveProperty('value', 'server effective body')
  })

  it('refreshes the active managed body', async () => {
    mocks.getSkillVersion
      .mockResolvedValueOnce(skillVersionDetail())
      .mockResolvedValueOnce({
        ...skillVersionDetail(),
        prompt: { ...skillVersionDetail().prompt, effective_body: 'refreshed effective body' },
      })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-test="system-prompt-refresh"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="system-prompt-body"]').element).toHaveProperty('value', 'refreshed effective body')
  })

  it('shows managed body unavailability without falling back to the legacy template', async () => {
    mocks.getSkillVersion.mockRejectedValue(new Error('detail unavailable'))
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-test="system-prompt-managed-unavailable"]').text()).toContain('The active version body is unavailable.')
    expect(wrapper.find('[data-test="system-prompt-body"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('seed')
  })

  it('reuses the loaded registry in the advanced drawer and closes it with Escape', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="system-prompt-open-advanced"]').trigger('click')
    await flushPromises()

    expect(mocks.getSkillRegistry).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-test="system-prompt-advanced-drawer"]').exists()).toBe(true)
    await wrapper.get('[data-test="system-prompt-advanced-drawer"]').trigger('keydown', { key: 'Escape' })
    expect(wrapper.find('[data-test="system-prompt-advanced-drawer"]').exists()).toBe(false)
  })

  it('uses the fixed ModelGang source and creates a candidate without publishing', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="system-prompt-open-advanced"]').trigger('click')
    await flushPromises()

    const drawer = wrapper.get('[data-test="system-prompt-advanced-drawer"]')
    const source = drawer.get('[data-test="system-prompt-skill-source"]')
    expect(source.text()).toContain('https://moxinggang.com/skills/security-research/current')
    expect(source.text()).toContain('https://codexrip.vip/skills/security-research/current')
    expect(drawer.text()).toContain('Fixed ModelGang source')
    expect(drawer.find('select').exists()).toBe(false)
    expect(drawer.text()).not.toContain('GitHub 官方')
    expect(drawer.text()).not.toContain('bootstrap-reverse-skill')
    expect(drawer.get('[title="Roll back"]').exists()).toBe(true)

    await drawer.get('[data-test="system-prompt-skill-sync"]').trigger('click')
    await flushPromises()
    expect(mocks.startSkillSync).toHaveBeenCalledWith(3, undefined)
    expect(mocks.publishSkillVersion).not.toHaveBeenCalled()
  })

  it('keeps a synced source candidate inactive until its publish action is explicit', async () => {
    const candidate = {
      ...skillRegistry().runtime.active!,
      id: 31,
      raw_tree_sha256: '5'.repeat(64),
      effective_tree_sha256: '6'.repeat(64),
      prompt_version_id: 41,
      prompt: {
        id: 41,
        raw_sha256: '7'.repeat(64),
        effective_sha256: '8'.repeat(64),
        diff: '--- raw\n+++ effective\n@@ routing\n-old\n+new',
        raw_body: 'candidate raw body',
        effective_body: 'candidate effective body',
        created_at: '2026-08-11T00:00:00Z',
      },
      file_changes: [{ path: 'SKILL.md', change: 'modified', kind: 'text' }],
      modified_files: 1,
      verified: true,
    }
    mocks.startSkillSync.mockResolvedValue({ id: 9, status: 'queued', progress_stage: 'queued', prompt_capture_provided: false, created_at: '2026-08-11T00:00:00Z' })
    mocks.getSkillSync.mockResolvedValue({ id: 9, status: 'succeeded', progress_stage: 'verified', prompt_capture_provided: false, candidate_bundle_version_id: 31, created_at: '2026-08-11T00:00:00Z' })
    const publishedRegistry = skillRegistry()
    publishedRegistry.runtime = { ...publishedRegistry.runtime, active: candidate, active_prompt: candidate.prompt, revision: 4 }
    publishedRegistry.versions = [candidate, ...publishedRegistry.versions]
    mocks.getSkillRegistry
      .mockResolvedValueOnce(skillRegistry())
      .mockResolvedValueOnce(skillRegistry())
      .mockResolvedValue(publishedRegistry)
    mocks.getSkillVersion.mockImplementation(async (id: number) => id === 30 ? skillVersionDetail() : candidate)
    mocks.publishSkillVersion.mockResolvedValue({ ...skillRegistry().runtime, active: candidate, active_prompt: candidate.prompt, revision: 4 })

    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="system-prompt-open-advanced"]').trigger('click')
    await flushPromises()
    vi.useFakeTimers()
    try {
      await wrapper.get('[data-test="system-prompt-skill-sync"]').trigger('click')
      await vi.advanceTimersByTimeAsync(1200)
      await flushPromises()

      const candidatePanel = wrapper.get('[data-test="system-prompt-skill-candidate"]')
      expect(candidatePanel.text()).toContain('Raw tree hash')
      expect(candidatePanel.text()).toContain('Effective tree hash')
      expect(candidatePanel.text()).toContain('Raw prompt hash')
      expect(candidatePanel.text()).toContain('Effective prompt hash')
      expect(candidatePanel.text()).toContain('Fetched at')
      expect(candidatePanel.text()).toContain('Exact prompt diff')
      expect(candidatePanel.get('[data-test="system-prompt-skill-prompt-diff"]').text()).toContain('@@ routing')
      expect(candidatePanel.text()).toContain('SKILL.md')
      expect(mocks.publishSkillVersion).not.toHaveBeenCalled()

      await candidatePanel.get('[data-test="system-prompt-skill-publish-candidate"]').trigger('click')
      await flushPromises()
      expect(mocks.publishSkillVersion).not.toHaveBeenCalled()
      expect(wrapper.get('[data-test="system-prompt-skill-confirm"]').exists()).toBe(true)
      await wrapper.get('[data-test="system-prompt-skill-confirm-action"]').trigger('click')
      await flushPromises()
      expect(mocks.publishSkillVersion).toHaveBeenCalledWith(31, 3, false)
      expect(wrapper.get('[data-test="system-prompt-body"]').element).toHaveProperty('value', 'candidate effective body')
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
    expect(wrapper.get('[data-test="system-prompt-body"]').element).toHaveProperty('readOnly', false)
    expect(wrapper.get('[data-test="system-prompt-tab-history"]').exists()).toBe(true)
  })

  it('keeps template creation reachable beside the mobile selector', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="system-prompt-mobile-create"]').trigger('click')

    expect(wrapper.text()).toContain('Identifier')
  })

  it('saves a new version separately from setting it current', async () => {
    mocks.saveDraft.mockResolvedValue({ ...version(2), id: 21, version: 2, body: 'draft', note: 'seed', is_active: false })
    mocks.publish.mockResolvedValue({ ...runtime(), template_id: 2, version_id: 21, template_version: 2, revision: 6 })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="system-prompt-template-2"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="system-prompt-body"]').setValue('draft')
    await wrapper.get('[data-test="system-prompt-save-version"]').trigger('click')
    await flushPromises()

    expect(mocks.saveDraft).toHaveBeenCalledWith(2, expect.objectContaining({ body: 'draft', expected_latest_version: 1, expected_revision: 5 }))
    expect(mocks.publish).not.toHaveBeenCalled()
    await wrapper.get('[data-test="system-prompt-set-current"]').trigger('click')
    await wrapper.get('[data-test="confirm-action"]').trigger('click')
    await flushPromises()
    expect(mocks.publish).toHaveBeenCalledWith(2, 21, 5, false)
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

  describe('editor request ownership', () => {
    it('keeps post-send body and note edits against the saved version without resubmitting or publishing', async () => {
      const save = deferred<ReturnType<typeof version>>()
      mocks.saveDraft.mockReturnValue(save.promise)
      const wrapper = mountView(); await flushPromises()
      await wrapper.get('[data-test="system-prompt-template-2"]').trigger('click'); await flushPromises()
      await wrapper.get('[data-test="system-prompt-body"]').setValue('sent body')
      await wrapper.get('[aria-label="Version note"]').setValue('sent note')
      await wrapper.get('[data-test="system-prompt-save-version"]').trigger('click')
      wrapper.get('[data-test="system-prompt-save-version"]').element.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await wrapper.get('[data-test="system-prompt-body"]').setValue('later body')
      await wrapper.get('[aria-label="Version note"]').setValue('later note')
      save.resolve({ ...version(2), id: 21, version: 2, body: 'sent body', note: 'sent note', is_active: false }); await flushPromises()

      expect(wrapper.get('[data-test="system-prompt-body"]').element).toHaveProperty('value', 'later body')
      expect(wrapper.get('[aria-label="Version note"]').element).toHaveProperty('value', 'later note')
      expect(mocks.saveDraft).toHaveBeenCalledTimes(1)
      expect(mocks.saveDraft.mock.calls[0]).toEqual([2, expect.objectContaining({ body: 'sent body', note: 'sent note', expected_latest_version: 1, expected_revision: 5 })])
      expect(wrapper.get('[data-test="system-prompt-set-current"]').attributes('disabled')).toBeDefined()
      expect(mocks.publish).not.toHaveBeenCalled()
      mocks.saveDraft.mockResolvedValueOnce({ ...version(2), id: 22, version: 3, body: 'later body', note: 'later note', is_active: false })
      await wrapper.get('[data-test="system-prompt-save-version"]').trigger('click'); await flushPromises()
      expect(mocks.saveDraft.mock.calls[1]).toEqual([2, expect.objectContaining({ body: 'later body', note: 'later note', expected_latest_version: 2, expected_revision: 5 })])
      expect(wrapper.get('[data-test="system-prompt-save-version"]').attributes('disabled')).toBeDefined()
      wrapper.unmount()
    })

    it('does not attach a saved version to a template selected after the request began', async () => {
      const save = deferred<ReturnType<typeof version>>()
      mocks.saveDraft.mockReturnValueOnce(save.promise)
      const wrapper = mountView(); await flushPromises()
      await wrapper.get('[data-test="system-prompt-template-2"]').trigger('click'); await flushPromises()
      await wrapper.get('[data-test="system-prompt-body"]').setValue('sent body')
      await wrapper.get('[data-test="system-prompt-save-version"]').trigger('click')
      // Return to the unchanged baseline so the existing dirty-selection
      // contract permits this explicit switch; no new discard rule is assumed.
      await wrapper.get('[data-test="system-prompt-body"]').setValue('seed')
      await wrapper.get('[data-test="system-prompt-template-1"]').trigger('click'); await flushPromises()
      save.resolve({ ...version(2), id: 21, version: 2, body: 'sent body', is_active: false }); await flushPromises()

      expect(wrapper.getComponent(SystemPromptAdvancedDrawer).props('sourceTemplate')).toMatchObject({ id: 1 })
      expect(wrapper.getComponent(SystemPromptAdvancedDrawer).props('sourceVersion')).toMatchObject({ id: 10, template_id: 1 })
      expect(wrapper.get('[data-test="system-prompt-body"]').element).toHaveProperty('value', 'server effective body')
      expect(mocks.publish).not.toHaveBeenCalled()
      expect(mocks.showSuccess).toHaveBeenCalledWith('A new prompt version has been saved.')
      wrapper.unmount()
    })

    it.each(['success', 'error'])('ignores an older template %s while a later selection is loading', async (outcome) => {
      mocks.list.mockResolvedValue({ templates: [codexTemplate(), gptTemplate(), thirdTemplate()], runtime: runtime() })
      const wrapper = mountView(); await flushPromises()
      const old = deferred<ReturnType<typeof templateDetail>>(), current = deferred<ReturnType<typeof templateDetail>>()
      mocks.get.mockImplementation((id: number) => id === 2 ? old.promise : current.promise)
      await wrapper.get('[data-test="system-prompt-template-2"]').trigger('click')
      await wrapper.get('[data-test="system-prompt-template-3"]').trigger('click')
      if (outcome === 'success') old.resolve(templateDetail(2))
      else old.reject(new Error('old template error'))
      await flushPromises()

      expect(wrapper.get('[data-test="system-prompt-refresh"]').attributes('disabled')).toBeDefined()
      expect(mocks.showError).not.toHaveBeenCalled()
      current.resolve(templateDetail(3)); await flushPromises()
      expect(wrapper.getComponent(SystemPromptAdvancedDrawer).props('sourceTemplate')).toMatchObject({ id: 3 })
      expect(wrapper.get('[data-test="system-prompt-body"]').element).toHaveProperty('value', 'third body')
      expect(wrapper.get('[data-test="system-prompt-refresh"]').attributes('disabled')).toBeUndefined()
      wrapper.unmount()
    })

    it('does not let an older list refresh select its old template after a newer explicit choice', async () => {
      const wrapper = mountView(); await flushPromises()
      const list = deferred<{ templates: ReturnType<typeof gptTemplate>[]; runtime: ReturnType<typeof runtime> }>()
      mocks.list.mockReturnValueOnce(list.promise)
      await wrapper.get('[data-test="system-prompt-refresh"]').trigger('click')
      await wrapper.get('[data-test="system-prompt-template-2"]').trigger('click'); await flushPromises()
      list.resolve({ templates: [codexTemplate(), gptTemplate()], runtime: runtime() }); await flushPromises()
      expect(wrapper.getComponent(SystemPromptAdvancedDrawer).props('sourceTemplate')).toMatchObject({ id: 2 })
      expect(mocks.get.mock.calls.map(call => call[0])).toEqual([1, 2])
      wrapper.unmount()
    })

    it('does not show an old managed-body response in a reopened managed selection', async () => {
      const old = deferred<ReturnType<typeof skillVersionDetail>>(), current = deferred<ReturnType<typeof skillVersionDetail>>()
      mocks.getSkillVersion.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
      const wrapper = mountView(); await flushPromises()
      await wrapper.get('[data-test="system-prompt-template-2"]').trigger('click'); await flushPromises()
      await wrapper.get('[data-test="system-prompt-template-1"]').trigger('click'); await flushPromises()
      old.resolve({ ...skillVersionDetail(), prompt: { ...skillVersionDetail().prompt, effective_body: 'old managed body' } }); await flushPromises()
      expect(wrapper.find('[data-test="system-prompt-managed-loading"]').exists()).toBe(true)
      expect(wrapper.find('[data-test="system-prompt-body"]').exists()).toBe(false)
      current.resolve(skillVersionDetail()); await flushPromises()
      expect(wrapper.get('[data-test="system-prompt-body"]').element).toHaveProperty('value', 'server effective body')
      wrapper.unmount()
    })
  })

  describe('sync polling lifetime', () => {
    it('does not schedule a poll from a sync-start response arriving after unmount', async () => {
      const start = deferred<{ id: number; status: string }>()
      mocks.startSkillSync.mockReturnValueOnce(start.promise)
      const wrapper = mountView(); await flushPromises()
      await wrapper.get('[data-test="system-prompt-open-advanced"]').trigger('click'); await flushPromises()
      vi.useFakeTimers()
      try {
        await wrapper.get('[data-test="system-prompt-skill-sync"]').trigger('click'); await flushPromises()
        wrapper.unmount()
        start.resolve({ id: 9, status: 'queued' }); await flushPromises()
        await vi.advanceTimersByTimeAsync(2400)
        expect(mocks.startSkillSync).toHaveBeenCalledTimes(1)
        expect(mocks.getSkillSync).not.toHaveBeenCalled()
        expect(mocks.publishSkillVersion).not.toHaveBeenCalled()
        const reopened = mountView(); await flushPromises()
        expect(reopened.get('[data-test="system-prompt-body"]').element).toHaveProperty('value', 'server effective body')
        expect(mocks.startSkillSync).toHaveBeenCalledTimes(1)
        reopened.unmount()
      } finally { vi.useRealTimers() }
    })

    it.each(['running', 'succeeded'])('does not continue a pending %s poll after unmount', async (status) => {
      const poll = deferred<{ id: number; status: string; candidate_bundle_version_id?: number }>()
      mocks.getSkillSync.mockReturnValueOnce(poll.promise)
      const wrapper = mountView(); await flushPromises()
      await wrapper.get('[data-test="system-prompt-open-advanced"]').trigger('click'); await flushPromises()
      vi.useFakeTimers()
      try {
        await wrapper.get('[data-test="system-prompt-skill-sync"]').trigger('click')
        await vi.advanceTimersByTimeAsync(1200)
        expect(mocks.getSkillSync).toHaveBeenCalledTimes(1)
        wrapper.unmount()
        poll.resolve({ id: 8, status, candidate_bundle_version_id: status === 'succeeded' ? 31 : undefined }); await flushPromises()
        await vi.advanceTimersByTimeAsync(2400)
        expect(mocks.getSkillSync).toHaveBeenCalledTimes(1)
        expect(mocks.getSkillVersion).toHaveBeenCalledTimes(1)
        expect(mocks.getSkillRegistry).toHaveBeenCalledTimes(1)
        expect(mocks.publishSkillVersion).not.toHaveBeenCalled()
        expect(mocks.showSuccess).not.toHaveBeenCalled()
      } finally { vi.useRealTimers() }
    })
  })
})
