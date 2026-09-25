import { beforeEach, describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), patch: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: client }))

import systemPromptsAPI from '../systemPrompts'
import { rulesAPI } from '../systemPromptRules'

describe('System Prompts API', () => {
  beforeEach(() => Object.values(client).forEach((mock) => mock.mockReset()))

  it('reads and atomically writes v2 content, rules and runtime through one config endpoint', async () => {
    client.get.mockResolvedValue({ data: { revision: 7 } })
    client.put.mockResolvedValue({ data: { revision: 8 } })
    await rulesAPI.config()
    expect(client.get).toHaveBeenCalledWith('/admin/system-prompts/config')
    const payload = { expected_revision: 7, enabled: true, expose_server_prompt: false, compact_enabled: true, policy: { version: 2, rules: [], default_rule_ids: [] }, contents: { custom: { body: 'Draft text' } } }
    await rulesAPI.saveConfig(payload)
    expect(client.put).toHaveBeenCalledWith('/admin/system-prompts/config', payload)
    expect(client.post).not.toHaveBeenCalled()
  })

  it('binds paired Skill publication to an explicit affected rule set and both revisions', async () => {
    client.post.mockResolvedValue({ data: {} })
    await systemPromptsAPI.publishSkillVersion(12, 3, false, { target_rule_ids: ['first', 'second'], expected_config_revision: 7 })
    expect(client.post).toHaveBeenCalledWith('/admin/system-prompts/skill-registry/versions/12/publish', { expected_revision: 3, expected_config_revision: 7, target_rule_ids: ['first', 'second'] })
  })

  it('uses the independent admin namespace', async () => {
    client.get.mockResolvedValue({ data: { templates: [], runtime: {} } })
    await systemPromptsAPI.list()
    expect(client.get).toHaveBeenCalledWith('/admin/system-prompts')

    client.get.mockResolvedValue({ data: { template: {}, versions: [], runtime: {} } })
    await systemPromptsAPI.get(12)
    expect(client.get).toHaveBeenCalledWith('/admin/system-prompts/12')

    client.get.mockResolvedValue({ data: [] })
    await systemPromptsAPI.listVersions(12)
    expect(client.get).toHaveBeenCalledWith('/admin/system-prompts/12/versions')

    expect(systemPromptsAPI).not.toHaveProperty('listBundles')
    expect(systemPromptsAPI).not.toHaveProperty('getBundle')

    client.get.mockResolvedValue({ data: { runtime: {}, versions: [] } })
    await systemPromptsAPI.getSkillRegistry()
    expect(client.get).toHaveBeenCalledWith('/admin/system-prompts/skill-registry')

    client.get.mockResolvedValue({ data: { id: 4, verified: true } })
    await systemPromptsAPI.getSkillVersion(4)
    expect(client.get).toHaveBeenCalledWith('/admin/system-prompts/skill-registry/versions/4')
  })

  it('passes expected revision on every catalog mutation', async () => {
    client.post.mockResolvedValue({ data: {} })
    client.patch.mockResolvedValue({ data: {} })
    client.delete.mockResolvedValue({ data: { deleted: true } })

    await systemPromptsAPI.create({ slug: 'custom', name: 'Custom', description: '', body: 'body', note: '', expected_revision: 7 })
    expect(client.post).toHaveBeenCalledWith('/admin/system-prompts', expect.objectContaining({ expected_revision: 7 }))

    await systemPromptsAPI.updateMetadata(12, { name: 'Renamed', expected_revision: 7 })
    expect(client.patch).toHaveBeenCalledWith('/admin/system-prompts/12', expect.objectContaining({ expected_revision: 7 }))

    await systemPromptsAPI.saveDraft(12, {
      body: 'draft', note: '', expected_latest_version: 3, expected_revision: 7,
      composition_mode: 'codex_skill_hybrid', bundle_id: 'codexrip-reverse-skill',
      bundle_manifest_sha256: '',
    })
    expect(client.post).toHaveBeenCalledWith('/admin/system-prompts/12/versions', expect.objectContaining({
      expected_latest_version: 3, expected_revision: 7, composition_mode: 'codex_skill_hybrid',
      bundle_id: 'codexrip-reverse-skill', bundle_manifest_sha256: '',
    }))

    await systemPromptsAPI.duplicate(12, { slug: 'custom-copy', name: 'Copy', expected_revision: 7 })
    expect(client.post).toHaveBeenCalledWith('/admin/system-prompts/12/duplicate', expect.objectContaining({ expected_revision: 7 }))

    await systemPromptsAPI.remove(12, 7)
    expect(client.delete).toHaveBeenCalledWith('/admin/system-prompts/12', { params: { expected_revision: 7 } })
  })

  it('keeps publish, rollback, and runtime updates explicit', async () => {
    client.post.mockResolvedValue({ data: {} })
    client.put.mockResolvedValue({ data: {} })

    await systemPromptsAPI.publish(12, 33, 7, false, ['custom'])
    expect(client.post).toHaveBeenCalledWith('/admin/system-prompts/12/versions/33/publish', { expected_revision: 7, target_rule_ids: ['custom'] })
    await systemPromptsAPI.publish(12, 22, 8, true, ['custom'])
    expect(client.post).toHaveBeenCalledWith('/admin/system-prompts/12/versions/22/rollback', { expected_revision: 8, target_rule_ids: ['custom'] })

    await systemPromptsAPI.updateRuntime({ expected_revision: 9, enabled: true, expose_server_prompt: false, compact_enabled: true })
    expect(client.put).toHaveBeenCalledWith('/admin/system-prompts/runtime', expect.objectContaining({ expected_revision: 9, enabled: true }))
  })

  it('syncs a managed prompt source without activating the candidate', async () => {
    client.post.mockResolvedValue({ data: { status: 'candidate_created', version: { id: 44 } } })

    await systemPromptsAPI.syncManagedSource(12, { expected_latest_version: 3, expected_revision: 7 })

    expect(client.post).toHaveBeenCalledWith('/admin/system-prompts/12/upstream-sync', {
      expected_latest_version: 3,
      expected_revision: 7,
    })
  })

  it('uses the independent skill registry revision for sync and publication', async () => {
    client.post.mockResolvedValue({ data: {} })
    client.get.mockResolvedValue({ data: {} })

    const promptCapture = new File(['capture'], 'capture.txt', { type: 'text/plain' })
    await systemPromptsAPI.startSkillSync(3, promptCapture)
    expect(client.post).toHaveBeenCalledTimes(1)
    const syncForm = client.post.mock.calls[0][1] as FormData
    expect(client.post.mock.calls[0][0]).toBe('/admin/system-prompts/skill-registry/syncs')
    expect(syncForm.get('expected_revision')).toBe('3')
    expect(syncForm.get('prompt_capture')).toBe(promptCapture)
    await systemPromptsAPI.getSkillSync(8)
    expect(client.get).toHaveBeenCalledWith('/admin/system-prompts/skill-registry/syncs/8')
    await systemPromptsAPI.publishSkillVersion(12, 3, false, { target_rule_ids: ['skill'], expected_config_revision: 8 })
    expect(client.post).toHaveBeenCalledWith('/admin/system-prompts/skill-registry/versions/12/publish', { expected_revision: 3, target_rule_ids: ['skill'], expected_config_revision: 8 })
    await systemPromptsAPI.publishSkillVersion(9, 4, true, { target_rule_ids: ['skill'], expected_config_revision: 9 })
    expect(client.post).toHaveBeenCalledWith('/admin/system-prompts/skill-registry/versions/9/rollback', { expected_revision: 4, target_rule_ids: ['skill'], expected_config_revision: 9 })
  })
})
