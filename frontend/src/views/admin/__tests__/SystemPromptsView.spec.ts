import { reactive } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import en from '@/i18n/locales/en/admin/systemPrompts'
import zh from '@/i18n/locales/zh/admin/systemPrompts'
import { promptConfig } from './systemPromptFixtures'

const api = vi.hoisted(() => ({ config: vi.fn(), saveConfig: vi.fn(), history: vi.fn(), preview: vi.fn(), list: vi.fn(), get: vi.fn(), getSkillRegistry: vi.fn(), getSkillVersion: vi.fn(), startSkillSync: vi.fn(), getSkillSync: vi.fn(), publishSkillVersion: vi.fn(), syncManagedSource: vi.fn() }))
const notify = vi.hoisted(() => ({ showSuccess: vi.fn() }))
vi.mock('@/api/admin/systemPromptRules', async () => ({ ...await vi.importActual<typeof import('@/api/admin/systemPromptRules')>('@/api/admin/systemPromptRules'), rulesAPI: api }))
vi.mock('@/stores/app', () => ({ useAppStore: () => notify }))
const authState = reactive({ user: { id: 1 } })
vi.mock('@/stores/auth', () => ({ useAuthStore: () => authState }))
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
  api.history.mockResolvedValue([{ id: 9, body: 'Historical body', composition_mode: 'inline', created_at: '2026-01-01T00:00:00Z', restorable: true }])
  authState.user.id = 1
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
  it('retains per-prompt drafts across selection, refresh, history and page remount', async () => {
    const wrapper = mountView(); await flushPromises()
    await wrapper.get('[data-test="system-prompt-body"]').setValue('Unsent first')
    await wrapper.get('[data-test="select-prompt-second"]').trigger('click')
    await wrapper.get('[data-test="prompt-name"]').setValue('Unsent second name')
    await wrapper.get('[data-test="system-prompt-refresh"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-test="prompt-open-history"]').trigger('click'); await flushPromises()
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
  it('restores a historical reference for the selected prompt through the unified save', async () => {
    const wrapper = mountView(); await flushPromises()
    await wrapper.get('[data-test="prompt-open-history"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-test="restore-version-9"]').trigger('click')
    expect(wrapper.get('[data-test="system-prompt-body"]').element).toHaveProperty('value', 'Historical body')
    api.saveConfig.mockImplementation(async payload => ({ ...promptConfig(), revision: 8, policy: payload.policy, contents: { ...promptConfig().contents, first: { ...promptConfig().contents.first!, body: 'Historical body', version_id: 9 } } }))
    await wrapper.get('[data-test="save-rules"]').trigger('click'); await flushPromises()
    expect(api.saveConfig).toHaveBeenCalledWith(expect.objectContaining({ contents: { first: { body: 'Historical body', restore_version_id: 9 } }, policy: expect.objectContaining({ rules: [expect.objectContaining({ id: 'first', version_id: 10 }), expect.objectContaining({ id: 'second', version_id: 20 })] }) }))
  })
  it.each(['envelope', 'array'])('edits only text in structured %s blocks and always shows retained restrictions', async shape => {
    const config = promptConfig()
    config.policy.rules[0] = { ...config.policy.rules[0]!, platforms: ['anthropic'], account_types: ['oauth'], request_profiles: ['generic-mimic'], exclude_model_contains: ['fable'] }
    const structured = { blocks: [{ type: 'text', text: 'First', enabled: true, cache_control: { type: 'ephemeral', ttl: '1h' } }, { type: 'text', text: 'Second', enabled: false }], expansion_prompt: 'Expansion' }
    config.contents.first = { ...config.contents.first!, composition_mode: 'anthropic_system_blocks', body: JSON.stringify(shape === 'array' ? structured.blocks : structured) }
    api.config.mockResolvedValue(config)
    const wrapper = mountView(); await flushPromises()
    expect(wrapper.find('[data-test="system-prompt-body"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="prompt-scope-summary"]').text()).toContain('generic-mimic')
    expect(wrapper.get('[data-test="prompt-scope-summary"]').text()).toContain('fable')
    await wrapper.get('[data-test="prompt-block-0"]').setValue('Changed text')
    await wrapper.get('[data-test="save-rules"]').trigger('click'); await flushPromises()
    const editedBlocks = [{ ...structured.blocks[0], text: 'Changed text' }, structured.blocks[1]]
    expect(JSON.parse(api.saveConfig.mock.lastCall![0].contents.first.body)).toEqual(shape === 'array' ? editedBlocks : { ...structured, blocks: editedBlocks })
  })
  it('loads history only for the selected rule and ignores obsolete responses', async () => {
    const wrapper = mountView(); await flushPromises()
    let finishFirst!: (value: unknown) => void
    api.history.mockImplementationOnce(() => new Promise(resolve => { finishFirst = resolve }))
    await wrapper.get('[data-test="prompt-open-history"]').trigger('click')
    await wrapper.get('[data-test="select-prompt-second"]').trigger('click'); await flushPromises()
    expect(api.history).toHaveBeenNthCalledWith(1, 'first')
    expect(api.history).toHaveBeenNthCalledWith(2, 'second')
    finishFirst([{ id: 88, body: 'Stale first history', composition_mode: 'inline', restorable: true }]); await flushPromises()
    expect(wrapper.text()).not.toContain('Stale first history')
    expect(wrapper.find('[data-test="history-template"]').exists()).toBe(false)
    await wrapper.get('[data-test="system-prompt-body"]').setValue('Keep unsaved')
    expect(wrapper.get('[data-test="restore-version-9"]').attributes('disabled')).toBeDefined()
  })
  it('saves an edited historical draft as private content while retaining its authorized metadata basis', async () => {
    const wrapper = mountView(); await flushPromises()
    await wrapper.get('[data-test="prompt-open-history"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-test="restore-version-9"]').trigger('click')
    await wrapper.get('[data-test="system-prompt-body"]').setValue('Historical body with my edit')
    api.saveConfig.mockResolvedValue(promptConfig())
    await wrapper.get('[data-test="save-rules"]').trigger('click'); await flushPromises()
    expect(api.saveConfig).toHaveBeenCalledWith(expect.objectContaining({ contents: { first: { body: 'Historical body with my edit', restore_version_id: 9 } } }))
  })
  it('keeps incompatible Skill archives read-only and removes legacy controls', async () => {
    api.history.mockResolvedValue([{ id: 88, body: 'Old scaffold', composition_mode: 'codex_skill_hybrid', created_at: '', restorable: false }])
    const wrapper = mountView(); await flushPromises()
    await wrapper.get('[data-test="prompt-open-history"]').trigger('click'); await flushPromises()
    expect(wrapper.find('[data-test="restore-version-88"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('Read-only archive')
    for (const selector of ['open-prompt-advanced', 'prompt-rules-preview', 'system-prompt-skill-source']) expect(wrapper.find(`[data-test="${selector}"]`).exists()).toBe(false)
    expect(api.getSkillRegistry).not.toHaveBeenCalled()
  })
  it('rejects a late save response after the administrator session changes', async () => {
    const wrapper = mountView(); await flushPromises()
    let finishSave!: (value: ReturnType<typeof promptConfig>) => void
    api.saveConfig.mockImplementationOnce(() => new Promise(resolve => { finishSave = resolve }))
    await wrapper.get('[data-test="system-prompt-body"]').setValue('Admin one content')
    await wrapper.get('[data-test="save-rules"]').trigger('click')
    const second = promptConfig(); second.contents.first!.body = 'Admin two server content'
    api.config.mockResolvedValue(second)
    authState.user.id = 2; await flushPromises()
    await wrapper.get('[data-test="system-prompt-body"]').setValue('Admin two draft')
    finishSave(savedConfig()); await flushPromises()
    expect(wrapper.get('[data-test="system-prompt-body"]').element).toHaveProperty('value', 'Admin two draft')
    expect(wrapper.get('[data-test="save-rules"]').attributes('disabled')).toBeUndefined()
  })
  it('keeps a pre-upgrade historical draft when migration replaces its content references', async () => {
    const baseline = promptConfig(), draft = promptConfig()
    draft.policy.rules[0]!.version_id = 9
    draft.contents.first!.version_id = 9
    draft.contents.first!.body = 'Unsaved historical restoration'
    sessionStorage.setItem('system-prompts-v2:1', JSON.stringify({ baseline, draft, selectedID: 'first', bodyBases: { first: 'Unsaved historical restoration', second: 'Second body' } }))
    const remote = promptConfig(); remote.revision = 8
    remote.policy.rules[0]!.template_id = 3; remote.policy.rules[0]!.version_id = 31
    remote.contents.first!.template_id = 3; remote.contents.first!.version_id = 31
    api.config.mockResolvedValue(remote)
    const wrapper = mountView(); await flushPromises()
    expect(wrapper.get('[data-test="system-prompt-body"]').element).toHaveProperty('value', 'Unsaved historical restoration')
    await wrapper.get('[data-test="keep-prompt-draft"]').trigger('click')
    api.saveConfig.mockResolvedValue(remote)
    await wrapper.get('[data-test="save-rules"]').trigger('click'); await flushPromises()
    expect(api.saveConfig).toHaveBeenCalledWith(expect.objectContaining({ expected_revision: 8, contents: { first: { body: 'Unsaved historical restoration' } }, policy: expect.objectContaining({ rules: expect.arrayContaining([expect.objectContaining({ id: 'first', template_id: 3, version_id: 31 })]) }) }))
  })
})
