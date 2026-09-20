import { beforeEach, describe, expect, it, vi } from 'vitest'
import { emptyEventFilters } from '../viewModel'
const call = vi.hoisted(() => vi.fn())
vi.mock('@sub2api/plugin-ui', () => ({ resource: call }))
import api from '../api'

describe('Prompt audit named resources', () => {
  beforeEach(() => call.mockReset())
  it('uses only declared resource names for config and runtime', async () => {
    await api.getConfig(); await api.getRuntime()
    expect(call.mock.calls).toEqual([['prompt-audit.config'], ['prompt-audit.runtime']])
  })
  it('passes an explicitly entered probe token without reading saved credentials', async () => {
    call.mockResolvedValue({ ok: true, token_applied: true })
    const result = await api.probeEndpoint({ id: 'guard-1', name: 'Guard', protocol: 'openai_compatible', base_url: 'http://127.0.0.1:8000', model: 'guard', token: 'synthetic-secret', clear_token: false, timeout_ms: 1000, input_limit: 1000, enabled: true, has_token: false, token_status: 'missing' })
    expect(call).toHaveBeenCalledWith('prompt-audit.probe', { body: { endpoint: expect.objectContaining({ token: 'synthetic-secret' }) } })
    expect(JSON.stringify(result)).not.toContain('synthetic-secret')
  })
  it('retains the server-issued deletion fence', async () => {
    await api.deleteEventsByFilter(emptyEventFilters(), { matched_count: 2, filter_summary: {}, snapshot_max_id: 10, filter_hash: 'a'.repeat(64), confirmation_token: 'opaque-token', expires_at: '2026-07-16T00:05:00Z' })
    expect(call).toHaveBeenCalledWith('prompt-audit.delete-by-filter', { body: expect.objectContaining({ snapshot_max_id: 10, filter_hash: 'a'.repeat(64), confirmation_token: 'opaque-token', confirm: true }) })
  })
})
