import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post, put } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get, post, put } }))

import { create, getModelContextCapacities, previewModelContextCapacities, syncUpstreamModels, update } from '@/api/admin/accounts'

describe('admin account context capacity API', () => {
  beforeEach(() => {
    get.mockReset().mockResolvedValue({ data: { capacity_rows: [] } })
    post.mockReset().mockResolvedValue({ data: { capacity_rows: [] } })
    put.mockReset().mockResolvedValue({ data: { id: 42 } })
  })

  it('reads local capacities with cancellation and returns the complete response', async () => {
    const signal = new AbortController().signal
    const result = { capacity_rows: [{ upstream_model_id: 'model' }] }
    get.mockResolvedValue({ data: result })
    expect(await getModelContextCapacities(42, signal)).toBe(result)
    expect(get).toHaveBeenCalledWith('/admin/accounts/42/models/context-capacities', { signal })
  })

  it('previews draft mappings locally without requiring a credential', async () => {
    const payload = { account_id: 42, platform: 'kimi', type: 'apikey', account_mode: 'coding', model_mapping: { alias: 'kimi-k3' }, model_ids: ['alias'] }
    const signal = new AbortController().signal
    await previewModelContextCapacities(payload, signal)
    expect(post).toHaveBeenCalledWith('/admin/accounts/models/context-capacities-preview', payload, { signal })
    expect(post.mock.calls[0][1]).not.toHaveProperty('api_key')
  })

  it('preserves capacity rows returned by explicit upstream synchronization', async () => {
    const result = { models: ['model'], metadata: { model: { context_window: 400_000 } }, capacity_rows: [{ upstream_model_id: 'model', upstream: { context_window: 400_000 } }] }
    post.mockResolvedValue({ data: result })
    expect(await syncUpstreamModels(42)).toBe(result)
  })

  it('sends named integer/null override patches at the top level', async () => {
    await update(42, { model_context_overrides: { one: 1_050_000, two: null } })
    expect(put).toHaveBeenCalledWith('/admin/accounts/42', { model_context_overrides: { one: 1_050_000, two: null } })
    const payload = { name: 'ordinary', platform: 'openai' as const, type: 'apikey' as const, credentials: {}, model_context_overrides: { one: 258_000 } }
    await create(payload)
    expect(post).toHaveBeenLastCalledWith('/admin/accounts', payload)
  })
})
