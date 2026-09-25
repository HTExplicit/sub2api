import { beforeEach, expect, it, vi } from 'vitest'
import { promptConfig } from '@/views/admin/__tests__/systemPromptFixtures'
const client = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn() }))
vi.mock('../../client', () => ({ apiClient: client }))
import prompts from '../systemPrompts'
beforeEach(() => { Object.values(client).forEach(mock => mock.mockReset()); client.get.mockResolvedValue({ data: [] }); client.put.mockResolvedValue({ data: promptConfig() }) })
it('writes through config and reads history by encoded stable rule identity', async () => {
  const payload = { expected_revision: 7, enabled: true, policy: promptConfig().policy, contents: { first: { body: 'restored', restore_version_id: 9 } } }
  await prompts.saveConfig(payload)
  expect(client.put).toHaveBeenCalledWith('/admin/system-prompts/config', payload)
  await prompts.history('rule with spaces')
  expect(client.get).toHaveBeenCalledWith('/admin/system-prompts/rules/rule%20with%20spaces/history')
  for (const retired of ['save', 'preview', 'publish', 'syncManagedSource', 'startSkillSync', 'publishSkillVersion', 'updateRuntime', 'create', 'saveDraft']) expect(prompts).not.toHaveProperty(retired)
})
