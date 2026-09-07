import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, nextTick, reactive, type EffectScope } from 'vue'
import type {
  ModelContextCapacitiesPreviewParams,
  ModelContextCapacitiesResult,
  ModelContextCapacityRow,
  SyncUpstreamModelsResult
} from '@/api/admin/accounts'

const { getModelContextCapacities, previewModelContextCapacities, syncUpstreamModels, syncUpstreamModelsPreview } = vi.hoisted(() => ({
  getModelContextCapacities: vi.fn(),
  previewModelContextCapacities: vi.fn(),
  syncUpstreamModels: vi.fn(),
  syncUpstreamModelsPreview: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: { getModelContextCapacities, previewModelContextCapacities, syncUpstreamModels, syncUpstreamModelsPreview }
  }
}))

import { useModelContextCapacities, withoutManagedCapacityExtra } from '../useModelContextCapacities'

const scopes: EffectScope[] = []

function row(overrides: Partial<ModelContextCapacityRow> = {}): ModelContextCapacityRow {
  return {
    upstream_model_id: 'real-model',
    aliases: ['public-model'],
    editable: true,
    automatic_context_window: 258_000,
    automatic_source: 'default',
    effective_context_window: 258_000,
    effective_source: 'default',
    capacity_basis: 'total_context',
    ...overrides
  }
}

function syncedResult(): SyncUpstreamModelsResult {
  return {
    models: ['real-model', 'dynamic-model'],
    capacity_rows: [row({
      upstream_model_id: 'dynamic-model',
      aliases: ['old-alias'],
      upstream: {
        context_window: 350_000,
        max_context_window: 500_000,
        max_output_tokens: 32_000,
        observed_at: '2026-09-07T10:00:00Z',
        capacity_basis: 'total_context'
      },
      automatic_context_window: 350_000,
      automatic_source: 'upstream',
      effective_context_window: 350_000,
      effective_source: 'upstream',
      max_context_window: 500_000,
      max_output_tokens: 32_000
    })]
  }
}

function setup(initial: Partial<ModelContextCapacitiesPreviewParams> = {}, enabled = true, syncIdentity = '') {
  const state = reactive({ enabled, identity: 'openai:apikey', syncIdentity })
  const params = reactive<ModelContextCapacitiesPreviewParams>({
    platform: 'openai',
    type: 'apikey',
    base_url: 'https://upstream.example/v1',
    model_mapping: { 'public-model': 'real-model' },
    model_ids: ['real-model'],
    ...initial
  })
  const scope = effectScope()
  scopes.push(scope)
  const capacities = scope.run(() => useModelContextCapacities({
    enabled: () => state.enabled,
    identity: () => state.identity,
    params: () => ({ ...params }),
    syncIdentity: () => state.syncIdentity
  }))!
  return { ...capacities, state, params, scope }
}

async function settle() {
  await nextTick()
  await Promise.resolve()
  await nextTick()
}

function deferred<T = ModelContextCapacitiesResult>() {
  let resolve!: (value: T) => void
  let reject!: (reason: Error) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

describe('useModelContextCapacities', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    getModelContextCapacities.mockReset().mockResolvedValue({ capacity_rows: [row()] })
    previewModelContextCapacities.mockReset().mockResolvedValue({ capacity_rows: [row()] })
    syncUpstreamModels.mockReset()
    syncUpstreamModelsPreview.mockReset()
  })

  afterEach(() => {
    for (const scope of scopes.splice(0)) scope.stop()
    vi.useRealTimers()
  })

  it('uses a local-only create preview without credentials, Extra or upstream synchronization', async () => {
    const model = setup()
    await settle()
    expect(previewModelContextCapacities).toHaveBeenCalledTimes(1)
    expect(previewModelContextCapacities).toHaveBeenCalledWith({
      platform: 'openai',
      type: 'apikey',
      base_url: 'https://upstream.example/v1',
      model_mapping: { 'public-model': 'real-model' },
      model_ids: ['real-model']
    }, expect.any(AbortSignal))
    expect(previewModelContextCapacities.mock.calls[0][0]).not.toHaveProperty('api_key')
    expect(previewModelContextCapacities.mock.calls[0][0]).not.toHaveProperty('extra')
    expect(getModelContextCapacities).not.toHaveBeenCalled()
    expect(syncUpstreamModels).not.toHaveBeenCalled()
    expect(syncUpstreamModelsPreview).not.toHaveBeenCalled()
    expect(model.rows.value).toEqual([row()])
    expect(model.loading.value).toBe(false)
  })

  it('starts editing with the persisted account GET and preserves its saved override', async () => {
    const saved = row({ custom_context_window: 1_050_000, effective_context_window: 1_050_000, effective_source: 'custom' })
    getModelContextCapacities.mockResolvedValueOnce({ capacity_rows: [saved] })
    const model = setup({ account_id: 17 })
    await settle()
    expect(getModelContextCapacities).toHaveBeenCalledTimes(1)
    expect(getModelContextCapacities).toHaveBeenCalledWith(17, expect.any(AbortSignal))
    expect(previewModelContextCapacities).not.toHaveBeenCalled()
    expect(model.rows.value).toEqual([saved])
    expect(model.buildPatch()).toEqual({})
  })

  it('debounces mapping changes while preserving drafts and real model identity', async () => {
    const model = setup()
    await settle()
    model.drafts.value = { 'real-model': '1.05M', 'draft-model': '' }
    model.params.model_mapping = { 'first-alias': 'real-model' }
    await nextTick()
    await vi.advanceTimersByTimeAsync(150)
    model.params.model_mapping = { 'second-alias': 'real-model' }
    await nextTick()
    await vi.advanceTimersByTimeAsync(249)
    expect(previewModelContextCapacities).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(previewModelContextCapacities).toHaveBeenCalledTimes(2)
    expect(previewModelContextCapacities.mock.calls[1][0]).toMatchObject({
      model_mapping: { 'second-alias': 'real-model' }, model_ids: ['real-model', 'draft-model']
    })
    expect(model.drafts.value).toEqual({ 'real-model': '1.05M', 'draft-model': '' })
  })

  it('aborts an older request and suppresses a late response even if the transport ignores abort', async () => {
    const oldRequest = deferred()
    const newRequest = deferred()
    previewModelContextCapacities.mockImplementationOnce(() => oldRequest.promise).mockImplementationOnce(() => newRequest.promise)
    const model = setup()
    const oldSignal = previewModelContextCapacities.mock.calls[0][1] as AbortSignal
    model.params.model_mapping = { fresh: 'fresh-model' }
    await nextTick()
    expect(oldSignal.aborted).toBe(true)
    await vi.advanceTimersByTimeAsync(250)
    const fresh = row({ upstream_model_id: 'fresh-model' })
    newRequest.resolve({ capacity_rows: [fresh] })
    await settle()
    expect(model.rows.value).toEqual([fresh])
    oldRequest.resolve({ capacity_rows: [row({ upstream_model_id: 'stale-model' })] })
    await settle()
    expect(model.rows.value).toEqual([fresh])
    expect(model.loadFailed.value).toBe(false)
    expect(model.loading.value).toBe(false)
  })

  it('does not turn an aborted stale error into a current failure', async () => {
    const oldRequest = deferred()
    previewModelContextCapacities.mockImplementationOnce(() => oldRequest.promise)
    const model = setup()
    model.params.model_mapping = { fresh: 'real-model' }
    await nextTick()
    await vi.advanceTimersByTimeAsync(250)
    oldRequest.reject(new Error('old aborted request'))
    await settle()
    expect(model.rows.value).toEqual([row()])
    expect(model.loadFailed.value).toBe(false)
  })

  it('retains actual create-sync evidence when an alias-only local preview cannot reproduce it', async () => {
    const model = setup()
    await settle()
    const synced = syncedResult()
    model.acceptSync(synced)
    model.drafts.value = { 'dynamic-model': '1M' }
    previewModelContextCapacities.mockResolvedValueOnce({
      capacity_rows: [row({ upstream_model_id: 'dynamic-model', aliases: ['new-alias'] })]
    })
    model.params.model_mapping = { 'new-alias': 'dynamic-model' }
    await nextTick()
    await vi.advanceTimersByTimeAsync(250)
    expect(previewModelContextCapacities.mock.calls[1][0].model_ids).toEqual(['real-model', 'dynamic-model'])
    expect(model.rows.value[0]).toMatchObject({
      aliases: ['new-alias'],
      upstream: synced.capacity_rows![0].upstream,
      automatic_source: 'upstream',
      automatic_context_window: 350_000,
      effective_source: 'upstream',
      effective_context_window: 350_000,
      max_context_window: 500_000,
      max_output_tokens: 32_000
    })
    expect(model.drafts.value).toEqual({ 'dynamic-model': '1M' })
    expect(model.buildPatch()).toEqual({ 'dynamic-model': 1_000_000 })
  })

  it('retains upstream evidence but does not replace a newer official automatic selection', async () => {
    const model = setup()
    await settle()
    const synced = syncedResult()
    model.acceptSync(synced)
    const official = row({
      upstream_model_id: 'dynamic-model', aliases: ['official-alias'],
      automatic_source: 'official', automatic_context_window: 1_050_000,
      effective_source: 'official', effective_context_window: 1_050_000
    })
    previewModelContextCapacities.mockResolvedValueOnce({ capacity_rows: [official] })
    await model.refresh()
    expect(model.rows.value[0]).toEqual({ ...official, upstream: synced.capacity_rows![0].upstream })
  })

  it('invalidates unsaved upstream evidence on endpoint change without discarding drafts', async () => {
    const model = setup()
    await settle()
    model.acceptSync(syncedResult())
    model.drafts.value = { 'dynamic-model': '1M' }
    previewModelContextCapacities.mockResolvedValueOnce({
      capacity_rows: [row({ upstream_model_id: 'dynamic-model', aliases: ['new-provider-alias'] })]
    })
    model.params.base_url = 'https://different-provider.example/v1'
    await nextTick()
    await vi.advanceTimersByTimeAsync(250)
    expect(model.rows.value[0].upstream).toBeUndefined()
    expect(model.rows.value[0].automatic_source).toBe('default')
    expect(model.rows.value[0].automatic_context_window).toBe(258_000)
    expect(model.drafts.value).toEqual({ 'dynamic-model': '1M' })
  })

  it('produces only editable override changes and treats protected rows as read-only', async () => {
    const model = setup()
    await settle()
    model.rows.value = [row(), row({ upstream_model_id: 'protected-model', editable: false, effective_source: 'protected' })]
    model.drafts.value = { 'real-model': '', 'protected-model': '2M' }
    expect(model.buildPatch()).toEqual({ 'real-model': null })
    model.rows.value = [row({ editable: false, effective_source: 'protected' })]
    model.drafts.value = { 'real-model': '1M' }
    expect(model.buildPatch()).toEqual({})
  })

  it('fails closed for unresolved drafts during profile refresh instead of silently dropping an override', async () => {
    const model = setup()
    await settle()
    model.drafts.value = { 'real-model': '1M' }
    expect(model.ready.value).toBe(true)
    const request = deferred()
    previewModelContextCapacities.mockImplementationOnce(() => request.promise)
    model.params.base_url = 'https://new-profile.example/v1'
    await nextTick()
    expect(model.rows.value).toEqual([])
    expect(model.ready.value).toBe(false)
    expect(() => model.buildPatch()).toThrow('Capacity metadata is not ready')
    await vi.advanceTimersByTimeAsync(250)
    expect(model.loading.value).toBe(true)
    expect(model.ready.value).toBe(false)
    request.resolve({ capacity_rows: [row()] })
    await settle()
    expect(model.ready.value).toBe(true)
    expect(model.buildPatch()).toEqual({ 'real-model': 1_000_000 })
    model.drafts.value = { 'unresolved-model': '1M' }
    expect(model.ready.value).toBe(false)
    expect(() => model.buildPatch()).toThrow('Capacity metadata is not ready')
  })

  it('does not let stale invalid drafts for a positively known protected row block saving', async () => {
    const model = setup()
    await settle()
    model.drafts.value = { 'real-model': 'invalid' }
    expect(model.valid.value).toBe(false)
    model.rows.value = [row({ editable: false, effective_source: 'protected' })]
    expect(model.ready.value).toBe(true)
    expect(model.valid.value).toBe(true)
    expect(model.buildPatch()).toEqual({})
    // Retain the draft in case the user returns to the ordinary provider, but do not submit it.
    expect(model.drafts.value).toEqual({ 'real-model': 'invalid' })
    model.rows.value = []
    expect(model.ready.value).toBe(false)
    expect(model.valid.value).toBe(false)
  })

  it('exposes invalid drafts and reports a current local failure without erasing drafts or rows', async () => {
    const model = setup()
    await settle()
    model.drafts.value = { 'real-model': '1.0000001M' }
    expect(model.valid.value).toBe(false)
    expect(() => model.buildPatch()).toThrow('Invalid context capacity')
    previewModelContextCapacities.mockRejectedValueOnce(new Error('local projection unavailable'))
    await model.refresh()
    expect(model.loadFailed.value).toBe(true)
    expect(model.loading.value).toBe(false)
    expect(model.rows.value).toEqual([row()])
    expect(model.drafts.value).toEqual({ 'real-model': '1.0000001M' })
  })

  it('resets stale state on account identity changes and when the modal closes', async () => {
    const model = setup({ account_id: 17 })
    await settle()
    model.drafts.value = { 'real-model': '1M' }
    model.acceptSync(syncedResult())
    model.state.identity = 'account:18'
    model.params.account_id = 18
    await settle()
    expect(getModelContextCapacities).toHaveBeenLastCalledWith(18, expect.any(AbortSignal))
    expect(model.drafts.value).toEqual({})
    expect(model.syncedModels.value).toBeUndefined()
    model.state.enabled = false
    await settle()
    expect(model.rows.value).toEqual([])
    expect(model.loading.value).toBe(false)
    expect(model.loadFailed.value).toBe(false)
  })

  it('does not request data while disabled and cancels both pending and active work on scope disposal', async () => {
    const model = setup({}, false)
    expect(previewModelContextCapacities).not.toHaveBeenCalled()
    model.state.enabled = true
    await settle()
    const firstSignal = previewModelContextCapacities.mock.calls[0][1] as AbortSignal
    model.params.model_mapping = { later: 'real-model' }
    await nextTick()
    expect(firstSignal.aborted).toBe(true)
    model.scope.stop()
    await vi.advanceTimersByTimeAsync(300)
    expect(previewModelContextCapacities).toHaveBeenCalledTimes(1)

    const active = setup()
    const activeSignal = previewModelContextCapacities.mock.calls[1][1] as AbortSignal
    active.scope.stop()
    expect(activeSignal.aborted).toBe(true)
  })

  it('accepts one explicit current sync callback, retains the full result and reprojects aliases locally', async () => {
    const model = setup({ account_id: 17, model_mapping: { 'current-alias': 'dynamic-model' } })
    await settle()
    model.drafts.value = { 'dynamic-model': '1.05M' }
    const originalMapping = { ...model.params.model_mapping }
    const originalModels = [...model.params.model_ids!]
    const upstream = deferred<SyncUpstreamModelsResult>()
    const local = deferred()
    previewModelContextCapacities.mockImplementationOnce(() => local.promise)
    const perform = vi.fn(() => upstream.promise)
    const ignoredDuplicate = vi.fn()
    const syncing = model.synchronize(perform)
    expect(perform.mock.calls).toEqual([[]])
    expect(model.syncing.value).toBe(true)
    expect(await model.synchronize(ignoredDuplicate)).toBeUndefined()
    expect(ignoredDuplicate).not.toHaveBeenCalled()
    const result: SyncUpstreamModelsResult = {
      ...syncedResult(),
      metadata: { 'dynamic-model': { id: 'dynamic-model', reasoning: true, supported_reasoning_levels: ['high'] } },
      warnings: [{ code: 'upstream_model_metadata_partial', message: 'Some model metadata is incomplete.' }]
    }
    upstream.resolve(result)
    expect(await syncing).toEqual(result)
    expect(model.syncedModels.value).toEqual(result)
    expect(model.syncing.value).toBe(false)
    expect(model.loading.value).toBe(true)
    expect(previewModelContextCapacities).toHaveBeenCalledTimes(1)
    expect(previewModelContextCapacities.mock.calls[0][0]).toMatchObject({
      account_id: 17,
      model_mapping: originalMapping,
      model_ids: ['real-model', 'dynamic-model']
    })
    expect(getModelContextCapacities).toHaveBeenCalledTimes(1)
    expect(syncUpstreamModels).not.toHaveBeenCalled()
    expect(syncUpstreamModelsPreview).not.toHaveBeenCalled()
    local.resolve({ capacity_rows: [row({ upstream_model_id: 'dynamic-model', aliases: ['current-alias'] })] })
    await settle()
    expect(model.rows.value[0]).toMatchObject({
      aliases: ['current-alias'], upstream: result.capacity_rows![0].upstream,
      automatic_source: 'upstream', automatic_context_window: 350_000
    })
    expect(model.drafts.value).toEqual({ 'dynamic-model': '1.05M' })
    expect(model.buildPatch()).toEqual({ 'dynamic-model': 1_050_000 })
    expect(model.params.model_mapping).toEqual(originalMapping)
    expect(model.params.model_ids).toEqual(originalModels)
  })

  it.each(['endpoint', 'mapping', 'account', 'close'] as const)(
    'ignores an outstanding sync after the %s changes', async (change) => {
      const model = setup()
      await settle()
      const upstream = deferred<SyncUpstreamModelsResult>()
      const perform = vi.fn(() => upstream.promise)
      const pending = model.synchronize(perform)
      expect(model.syncing.value).toBe(true)
      if (change === 'endpoint') model.params.base_url = 'https://different-provider.example/v1'
      else if (change === 'mapping') model.params.model_mapping = { 'next-alias': 'next-model' }
      else if (change === 'account') model.state.identity = 'next-account'
      else model.state.enabled = false
      await settle()
      const expectedRows = [...model.rows.value]
      const localCallCount = previewModelContextCapacities.mock.calls.length
      expect(model.syncing.value).toBe(false)
      upstream.resolve(syncedResult())
      expect(await pending).toBeUndefined()
      await settle()
      expect(model.syncedModels.value).toBeUndefined()
      expect(model.rows.value).toEqual(expectedRows)
      expect(model.syncing.value).toBe(false)
      expect(previewModelContextCapacities).toHaveBeenCalledTimes(localCallCount)
    }
  )

  it('does not clear a newer sync state when a previous provider sync completes', async () => {
    const model = setup()
    await settle()
    const oldUpstream = deferred<SyncUpstreamModelsResult>()
    const newUpstream = deferred<SyncUpstreamModelsResult>()
    const oldPending = model.synchronize(() => oldUpstream.promise)
    model.params.base_url = 'https://new-provider.example/v1'
    await settle()
    const newPending = model.synchronize(() => newUpstream.promise)
    expect(model.syncing.value).toBe(true)
    oldUpstream.resolve(syncedResult())
    expect(await oldPending).toBeUndefined()
    expect(model.syncing.value).toBe(true)
    expect(model.syncedModels.value).toBeUndefined()
    const newResult = { models: ['next-model'], capacity_rows: [row({ upstream_model_id: 'next-model' })] }
    newUpstream.resolve(newResult)
    expect(await newPending).toEqual(newResult)
    expect(model.syncing.value).toBe(false)
    expect(model.syncedModels.value).toEqual(newResult)
  })

  it('clears raw evidence and rejects a pending sync after a credential identity change without sending the key locally', async () => {
    const model = setup({}, true, 'test-credential-before')
    await settle()
    model.acceptSync(syncedResult())
    model.drafts.value = { 'dynamic-model': '1M' }
    const pendingUpstream = deferred<SyncUpstreamModelsResult>()
    const pending = model.synchronize(() => pendingUpstream.promise)
    model.state.syncIdentity = 'test-credential-after'
    await settle()
    expect(model.syncedModels.value).toBeUndefined()
    expect(model.rows.value).toEqual([])
    expect(model.drafts.value).toEqual({ 'dynamic-model': '1M' })
    expect(model.profileChanged.value).toBe(true)
    await vi.advanceTimersByTimeAsync(250)
    const localPayload = previewModelContextCapacities.mock.calls[1][0]
    expect(localPayload).not.toHaveProperty('api_key')
    expect(localPayload).not.toHaveProperty('credentials')
    expect(localPayload).not.toHaveProperty('syncIdentity')
    expect(JSON.stringify(localPayload)).not.toContain('test-credential')
    pendingUpstream.resolve(syncedResult())
    expect(await pending).toBeUndefined()
    expect(model.syncedModels.value).toBeUndefined()
    expect(model.rows.value.every(item => !item.upstream)).toBe(true)
  })

  it('tracks unsaved edit endpoint and credential changes separately from model mapping changes', async () => {
    const model = setup({ account_id: 17 }, true, 'test-original-credential')
    await settle()
    expect(model.profileChanged.value).toBe(false)
    model.params.model_mapping = { 'other-alias': 'real-model' }
    await settle()
    expect(model.profileChanged.value).toBe(false)
    model.params.base_url = 'https://changed.example/v1'
    await settle()
    expect(model.profileChanged.value).toBe(true)
    model.params.base_url = 'https://upstream.example/v1'
    await settle()
    expect(model.profileChanged.value).toBe(false)
    model.state.syncIdentity = 'test-changed-credential'
    await settle()
    expect(model.profileChanged.value).toBe(true)
  })

  it('strips all three managed Extra keys without changing unrelated settings or the caller object', () => {
    const extra = {
      model_context_overrides: { 'real-model': 1_000_000 },
      upstream_model_context_capacities: { old: true },
      upstream_model_metadata: { old: true },
      openai_reasoning_signature_recovery_enabled: false,
      nested: { keep: true }
    }
    expect(withoutManagedCapacityExtra(extra)).toEqual({ openai_reasoning_signature_recovery_enabled: false, nested: { keep: true } })
    expect(extra).toHaveProperty('model_context_overrides')
    expect(extra).toHaveProperty('upstream_model_context_capacities')
    expect(extra).toHaveProperty('upstream_model_metadata')
  })
})
