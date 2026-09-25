import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import en from '@/i18n/locales/en/admin/systemPrompts'
import zh from '@/i18n/locales/zh/admin/systemPrompts'
import { promptConfig, template, version } from './systemPromptFixtures'

const api = vi.hoisted(() => ({ config: vi.fn(), saveConfig: vi.fn(), preview: vi.fn(), list: vi.fn(), get: vi.fn(), getSkillRegistry: vi.fn(), getSkillVersion: vi.fn(), startSkillSync: vi.fn(), getSkillSync: vi.fn(), publishSkillVersion: vi.fn(), syncManagedSource: vi.fn() }))
const notify = vi.hoisted(() => ({ showSuccess: vi.fn() }))
vi.mock('@/api/admin/systemPromptRules', async () => ({ ...await vi.importActual<typeof import('@/api/admin/systemPromptRules')>('@/api/admin/systemPromptRules'), rulesAPI: api }))
vi.mock('@/api/admin/systemPrompts', () => ({ default: api }))
vi.mock('@/stores/app', () => ({ useAppStore: () => notify }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { id: 1 } }) }))
import SystemPromptsView from '../SystemPromptsView.vue'

const wrappers: VueWrapper[] = []
function mountView() {
  const wrapper = mount(SystemPromptsView, { global: { plugins: [createI18n({ legacy: false, locale: 'en', messages: { en: { admin: en }, zh: { admin: zh } } })], stubs: { AppLayout: { template: '<div><slot /></div>' }, Icon: true, teleport: true, transition: false } } })
  wrappers.push(wrapper)
  return wrapper
}
function savedConfig() {
  const next = promptConfig()
  next.revision = 8
  next.policy.rules[0]!.name = 'Renamed'
  next.policy.rules[0]!.role = 'developer'
  next.policy.rules[0]!.position = 'after_last_user'
  next.policy.rules[0]!.version_id = 11
  next.policy.default_rule_ids = []
  next.contents.first = { ...next.contents.first!, body: 'Edited text', version_id: 11 }
  return next
}
beforeEach(() => {
  sessionStorage.clear()
  Object.values(api).forEach(mock => mock.mockReset())
  api.config.mockResolvedValue(promptConfig())
  api.list.mockResolvedValue({ templates: [template] })
  api.get.mockResolvedValue({ template, versions: [version], runtime: {} })
  api.getSkillRegistry.mockResolvedValue({ runtime: { revision: 3 }, versions: [], source: {} })
})
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); vi.restoreAllMocks() })

describe('unified prompt configuration', () => {
  it('saves content, settings and defaults in one revision-checked operation', async () => {
    api.saveConfig.mockResolvedValue(savedConfig())
    const wrapper = mountView(); await flushPromises()
    expect(api.getSkillRegistry).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="system-prompt-set-current"]').exists()).toBe(false)
    await wrapper.get('[data-test="prompt-name"]').setValue('Renamed')
    await wrapper.get('[data-test="system-prompt-body"]').setValue('Edited text')
    await wrapper.get('[data-test="prompt-role"]').setValue('developer')
    await wrapper.get('[data-test="prompt-position"]').setValue('after_last_user')
    await wrapper.get('[data-test="rule-default"]').setValue(false)
    await wrapper.get('[data-test="save-rules"]').trigger('click'); await flushPromises()
    expect(api.saveConfig).toHaveBeenCalledTimes(1)
    expect(api.saveConfig).toHaveBeenCalledWith(expect.objectContaining({ expected_revision: 7, contents: { first: { body: 'Edited text' } }, policy: expect.objectContaining({ version: 2, default_rule_ids: [], rules: [expect.objectContaining({ id: 'first', name: 'Renamed', role: 'developer', position: 'after_last_user' }), expect.objectContaining({ id: 'second' })] }) }))
    expect(wrapper.get('[data-test="save-rules"]').attributes('disabled')).toBeDefined()
  })
  it('retains per-prompt drafts across selection, refresh, advanced close and page remount', async () => {
    const wrapper = mountView(); await flushPromises()
    await wrapper.get('[data-test="system-prompt-body"]').setValue('Unsent first')
    await wrapper.get('[data-test="select-prompt-second"]').trigger('click')
    await wrapper.get('[data-test="prompt-name"]').setValue('Unsent second name')
    await wrapper.get('[data-test="system-prompt-refresh"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-test="open-prompt-advanced"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-test="system-prompt-advanced-drawer"]').trigger('keydown', { key: 'Escape' })
    await wrapper.get('[data-test="select-prompt-first"]').trigger('click')
    expect(wrapper.get('[data-test="system-prompt-body"]').element).toHaveProperty('value', 'Unsent first')
    wrapper.unmount(); wrappers.splice(wrappers.indexOf(wrapper), 1)
    const next = mountView(); await flushPromises()
    expect(next.get('[data-test="system-prompt-body"]').element).toHaveProperty('value', 'Unsent first')
    await next.get('[data-test="select-prompt-second"]').trigger('click')
    expect(next.get('[data-test="prompt-name"]').element).toHaveProperty('value', 'Unsent second name')
    expect(api.saveConfig).not.toHaveBeenCalled()
  })
  it('retains edits made while refresh and save responses are pending', async () => {
    const wrapper = mountView(); await flushPromises()
    let finishRefresh!: (value: ReturnType<typeof promptConfig>) => void
    api.config.mockImplementationOnce(() => new Promise(resolve => { finishRefresh = resolve }))
    await wrapper.get('[data-test="system-prompt-refresh"]').trigger('click')
    await wrapper.get('[data-test="system-prompt-body"]').setValue('Edited text')
    finishRefresh(promptConfig()); await flushPromises()
    expect(wrapper.get('[data-test="system-prompt-body"]').element).toHaveProperty('value', 'Edited text')
    let finishSave!: (value: ReturnType<typeof promptConfig>) => void
    api.saveConfig.mockImplementationOnce(() => new Promise(resolve => { finishSave = resolve }))
    await wrapper.get('[data-test="save-rules"]').trigger('click')
    await wrapper.get('[data-test="system-prompt-body"]').setValue('Typed during save')
    const saved = promptConfig(); saved.revision = 8; saved.contents.first!.body = 'Edited text'; saved.contents.first!.version_id = 11; saved.policy.rules[0]!.version_id = 11
    finishSave(saved); await flushPromises()
    expect(wrapper.get('[data-test="system-prompt-body"]').element).toHaveProperty('value', 'Typed during save')
    expect(wrapper.get('[data-test="save-rules"]').attributes('disabled')).toBeUndefined()
  })
  it('keeps a conflicted draft and merges untouched server changes only after explicit review', async () => {
    const wrapper = mountView(); await flushPromises()
    await wrapper.get('[data-test="prompt-name"]').setValue('Local name')
    await wrapper.get('[data-test="system-prompt-body"]').setValue('Local body')
    const remote = promptConfig(); remote.revision = 8; remote.policy.rules[1]!.name = 'Remote second name'; remote.policy.rules[0]!.models = ['gpt-6-astra']
    api.config.mockResolvedValue(remote)
    api.saveConfig.mockRejectedValue({ response: { data: { code: 'system_prompt_revision_conflict' }, status: 409 } })
    await wrapper.get('[data-test="save-rules"]').trigger('click'); await flushPromises()
    expect(wrapper.get('[data-test="system-prompt-body"]').element).toHaveProperty('value', 'Local body')
    expect(wrapper.get('[data-test="save-rules"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-test="keep-prompt-draft"]').trigger('click')
    expect(wrapper.text()).toContain('Remote second name')
    expect(wrapper.get('[data-test="prompt-name"]').element).toHaveProperty('value', 'Local name')
    expect(wrapper.get('[data-test="prompt-models"]').element).toHaveProperty('value', 'gpt-6-astra')
    api.saveConfig.mockImplementation(async payload => ({ ...remote, policy: payload.policy, contents: { ...remote.contents, first: { ...remote.contents.first!, body: 'Local body' } }, revision: 9 }))
    await wrapper.get('[data-test="save-rules"]').trigger('click'); await flushPromises()
    expect(api.saveConfig.mock.lastCall?.[0].expected_revision).toBe(8)
  })
  it('requires an explicit platform and starts with automatic control append', async () => {
    const wrapper = mountView(); await flushPromises()
    await wrapper.get('[data-test="add-prompt"]').trigger('click')
    expect(wrapper.get('[data-test="prompt-role"]').element).toHaveProperty('value', 'auto')
    expect(wrapper.get('[data-test="prompt-position"]').element).toHaveProperty('value', 'control_append')
    await wrapper.get('[data-test="prompt-name"]').setValue('New')
    await wrapper.get('[data-test="system-prompt-body"]').setValue('New body')
    expect(wrapper.get('[data-test="save-rules"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-test="platform-gemini"]').setValue(true)
    expect(wrapper.get('[data-test="save-rules"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('[data-test="prompt-role"] option[value="developer"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-test="prompt-position"] option[value="after_last_user"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-test="prompt-role"] option[value="system"]').attributes('disabled')).toBeUndefined()
  })
  it('retains a content-only draft when another administrator deleted its rule', async () => {
    const wrapper = mountView(); await flushPromises()
    await wrapper.get('[data-test="system-prompt-body"]').setValue('Locally edited after remote deletion')
    const remote = promptConfig(); remote.revision = 8; remote.policy.rules = remote.policy.rules.filter(rule => rule.id !== 'first'); remote.policy.default_rule_ids = []; delete remote.contents.first
    api.config.mockResolvedValue(remote)
    await wrapper.get('[data-test="system-prompt-refresh"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-test="keep-prompt-draft"]').trigger('click')
    expect(wrapper.get('[data-test="system-prompt-body"]').element).toHaveProperty('value', 'Locally edited after remote deletion')
    api.saveConfig.mockImplementation(async payload => ({ ...remote, revision: 9, policy: payload.policy, contents: { ...remote.contents, first: { ...promptConfig().contents.first!, body: payload.contents.first.body } } }))
    await wrapper.get('[data-test="save-rules"]').trigger('click'); await flushPromises()
    expect(api.saveConfig).toHaveBeenCalledWith(expect.objectContaining({ expected_revision: 8, contents: { first: { body: 'Locally edited after remote deletion' } }, policy: expect.objectContaining({ rules: expect.arrayContaining([expect.objectContaining({ id: 'first', template_id: 1 })]) }) }))
  })
  it('preserves source-managed text and copies it into an independent draft', async () => {
    const config = promptConfig(); config.contents.first!.managed = true
    api.config.mockResolvedValue(config)
    const wrapper = mountView(); await flushPromises()
    expect(wrapper.get('[data-test="system-prompt-body"]').attributes('readonly')).toBeDefined()
    await wrapper.get('[data-test="duplicate-prompt"]').trigger('click')
    expect(wrapper.get('[data-test="system-prompt-body"]').attributes('readonly')).toBeUndefined()
    expect(wrapper.get('[data-test="system-prompt-body"]').element).toHaveProperty('value', 'First body')
    expect(wrapper.get('[data-test="platform-openai"]').element).toHaveProperty('checked', false)
    expect(api.saveConfig).not.toHaveBeenCalled()
  })
  it('previews unsaved content and reports the final fields and missing user anchor', async () => {
    api.preview.mockResolvedValue({ requested_model: 'alias', upstream_model: 'mapped', protocol: 'responses', transport: 'http', body: { instructions: 'Client instructions', input: [{ role: 'developer', content: 'Unsent' }] }, before_rules: {}, client_control: {}, gateway_base_instructions: '', simulated: true, wire_verified: true, application: { applied: true, rules_plan: { placements: [{ rule_id: 'first', position: 'conversation_tail', carrier: 'input', role: 'developer', body: 'Unsent', index: 1 }], skipped: [{ rule_id: 'second', reason: 'last_user_missing' }] } } })
    const wrapper = mountView(); await flushPromises()
    await wrapper.get('[data-test="system-prompt-body"]').setValue('Unsent')
    await wrapper.get('input[type="number"]').setValue(42)
    await wrapper.get('[data-test="run-prompt-preview"]').trigger('click'); await flushPromises()
    expect(api.preview).toHaveBeenCalledWith(42, expect.objectContaining({ contents: { first: { body: 'Unsent' } }, policy: expect.objectContaining({ version: 2 }) }))
    expect(wrapper.text()).toContain('role=developer [1]')
    expect(wrapper.text()).toContain('No user message in this outbound request')
    expect(wrapper.text()).toContain('Top-level instructions')
    expect(wrapper.text()).toContain('Outbound message array')
    expect(api.saveConfig).not.toHaveBeenCalled()
  })
  it('restores a historical reference for the selected prompt through the unified save', async () => {
    const wrapper = mountView(); await flushPromises()
    await wrapper.get('[data-test="open-prompt-advanced"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-test="restore-version-9"]').trigger('click')
    expect(wrapper.get('[data-test="system-prompt-body"]').element).toHaveProperty('value', 'Historical body')
    api.saveConfig.mockImplementation(async payload => ({ ...promptConfig(), revision: 8, policy: payload.policy, contents: { ...promptConfig().contents, first: { ...promptConfig().contents.first!, body: 'Historical body', version_id: 9 } } }))
    await wrapper.get('[data-test="save-rules"]').trigger('click'); await flushPromises()
    expect(api.saveConfig).toHaveBeenCalledWith(expect.objectContaining({ contents: {}, policy: expect.objectContaining({ rules: [expect.objectContaining({ id: 'first', version_id: 9 }), expect.objectContaining({ id: 'second', version_id: 20 })] }) }))
  })
  it('shows every affected prompt before paired Skill publication and sends explicit targets', async () => {
    const config = promptConfig()
    config.policy.rules[1]!.template_id = 1
    config.contents.first!.managed = true
    config.contents.second!.managed = true
    config.contents.first!.composition_mode = 'codex_skill_hybrid'
    config.contents.second!.composition_mode = 'codex_skill_hybrid'
    api.config.mockResolvedValue(config)
    api.list.mockResolvedValue({ templates: [{ ...template, managed_source: 'remote_skill_registry' }] })
    api.get.mockResolvedValue({ template: { ...template, managed_source: 'remote_skill_registry' }, versions: [version], runtime: {} })
    api.getSkillRegistry.mockResolvedValue({ runtime: { revision: 3, active: { id: 30, raw_tree_sha256: 'raw', effective_tree_sha256: 'tree', effective_total_bytes: 10, file_count: 1 } }, versions: [{ id: 29, effective_tree_sha256: 'older-tree', file_count: 1 }], source: {} })
    api.publishSkillVersion.mockResolvedValue({})
    const wrapper = mountView(); await flushPromises()
    await wrapper.get('[data-test="open-prompt-advanced"]').trigger('click'); await flushPromises()
    expect(wrapper.get('[data-test="skill-target-rules"]').text()).toContain('First prompt, Second prompt')
    await wrapper.get('[title="Roll back"]').trigger('click')
    expect(api.publishSkillVersion).not.toHaveBeenCalled()
    await wrapper.get('[data-test="system-prompt-skill-confirm-action"]').trigger('click'); await flushPromises()
    expect(api.publishSkillVersion).toHaveBeenCalledWith(29, 3, true, { target_rule_ids: ['first', 'second'], expected_config_revision: 7 })
  })
  it('subscribes a new prompt to the current paired Skill without copying an old scaffold into a new version', async () => {
    const source = { ...template, id: 3, managed_source: 'remote_skill_registry' }
    api.list.mockResolvedValue({ templates: [template, source] })
    api.get.mockImplementation(async id => id === 3 ? { template: source, versions: [{ ...version, id: 31, template_id: 3, body: 'Old scaffold', composition_mode: 'codex_skill_hybrid' }], runtime: {} } : { template, versions: [version], runtime: {} })
    api.getSkillRegistry.mockResolvedValue({ runtime: { revision: 3, active: { id: 30, effective_total_bytes: 10 } }, versions: [], source: {} })
    api.getSkillVersion.mockResolvedValue({ id: 30, prompt: { effective_body: 'Current paired prompt' } })
    const wrapper = mountView(); await flushPromises()
    await wrapper.get('[data-test="open-prompt-advanced"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-test="subscribe-current-skill"]').trigger('click'); await flushPromises()
    expect(wrapper.get('[data-test="system-prompt-body"]').element).toHaveProperty('value', 'Current paired prompt')
    expect(wrapper.get('[data-test="system-prompt-body"]').attributes('readonly')).toBeDefined()
    expect(wrapper.get('[data-test="prompt-role"]').element).toHaveProperty('value', 'auto')
    expect(wrapper.get('[data-test="platform-openai"]').element).toHaveProperty('checked', false)
    await wrapper.get('[data-test="platform-openai"]').setValue(true)
    api.saveConfig.mockImplementation(async payload => {
      const added = payload.policy.rules.find((rule: { template_id: number }) => rule.template_id === 3)
      return { ...promptConfig(), revision: 8, policy: payload.policy, contents: { ...promptConfig().contents, [added.id]: { body: 'Current paired prompt', template_id: 3, version_id: 31, composition_mode: 'codex_skill_hybrid', managed: true } } }
    })
    await wrapper.get('[data-test="save-rules"]').trigger('click'); await flushPromises()
    expect(api.saveConfig).toHaveBeenCalledWith(expect.objectContaining({ contents: {}, policy: expect.objectContaining({ rules: expect.arrayContaining([expect.objectContaining({ template_id: 3, version_id: 31, role: 'auto', position: 'control_append', platforms: ['openai'] })]) }) }))
    expect(api.publishSkillVersion).not.toHaveBeenCalled()
  })
  it('shows an unavailable managed source while allowing its settings and global switch to be saved', async () => {
    const config = promptConfig(); config.contents.first = { ...config.contents.first!, body: '', managed: true, composition_mode: 'codex_skill_hybrid', available: false }
    api.config.mockResolvedValue(config)
    api.saveConfig.mockImplementation(async payload => ({ ...config, revision: 8, enabled: payload.enabled, policy: payload.policy }))
    const wrapper = mountView(); await flushPromises()
    expect(wrapper.get('[data-test="managed-source-unavailable"]').text()).toContain('current paired source is unavailable')
    expect(wrapper.find('[data-test="system-prompt-body"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="duplicate-prompt"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-test="prompt-name"]').setValue('Unavailable source')
    await wrapper.get('[data-test="system-prompt-global-toggle"] input').setValue(false)
    await wrapper.get('[data-test="save-rules"]').trigger('click'); await flushPromises()
    expect(api.saveConfig).toHaveBeenCalledWith(expect.objectContaining({ enabled: false, contents: {}, policy: expect.objectContaining({ rules: expect.arrayContaining([expect.objectContaining({ name: 'Unavailable source' })]) }) }))
  })
})
