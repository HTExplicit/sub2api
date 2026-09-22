import { beforeEach, describe, expect, it, vi } from 'vitest'
import plugins from '../plugins'

const client = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: client }))
const digest = 'a'.repeat(64)
const version = { revision: 4, package_sha256: digest }
beforeEach(() => vi.clearAllMocks())

describe('plugin version wire contract', () => {
  it('saves raw JSON with its read version and preserves a canonical header receipt', async () => {
    const raw = { code: 17, enabled: true }
    client.put.mockResolvedValue({ data: raw, headers: { 'x-sub2api-plugin-revision': '5', 'x-sub2api-plugin-package': digest } })
    expect(await plugins.saveConfig(7, raw, version)).toEqual({ config: raw, revision: 5, package_sha256: digest })
    expect(client.put).toHaveBeenCalledWith('/admin/plugins/7/config', raw, { rawPluginConfig: true, headers: { 'X-Sub2API-Plugin-Revision': '4', 'X-Sub2API-Plugin-Package': digest } })
    expect(client.get).not.toHaveBeenCalled()
  })

  it('rejects missing or unrelated response revision instead of inventing a newer receipt', async () => {
    client.put.mockResolvedValue({ data: {}, headers: { 'x-sub2api-plugin-revision': '9', 'x-sub2api-plugin-package': digest } })
    await expect(plugins.saveConfig(7, {}, version)).rejects.toThrow('receipt')
    client.get.mockResolvedValue({ data: {}, headers: {} })
    await expect(plugins.getConfig(7, digest)).rejects.toThrow('Reload')
  })

  it('sends lifecycle snapshot headers without a new read', async () => {
    client.post.mockResolvedValue({ data: {} })
    client.delete.mockResolvedValue({})
    const plugin = { id: 7, ...version }
    await plugins.enable(plugin, 37, false)
    await plugins.disable(plugin)
    await plugins.test(plugin)
    await plugins.remove(plugin)
    for (const call of client.post.mock.calls) expect(call[2]).toEqual({ headers: { 'X-Sub2API-Plugin-Revision': '4', 'X-Sub2API-Plugin-Package': digest } })
    expect(client.delete).toHaveBeenCalledWith('/admin/plugins/7', { headers: { 'X-Sub2API-Plugin-Revision': '4', 'X-Sub2API-Plugin-Package': digest } })
    expect(client.get).not.toHaveBeenCalled()
  })
})
