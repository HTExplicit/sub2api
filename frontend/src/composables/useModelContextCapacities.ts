import { computed, onScopeDispose, ref, watch } from 'vue'
import { adminAPI } from '@/api/admin'
import type {
  ModelContextCapacitiesPreviewParams,
  ModelContextCapacityRow,
  SyncUpstreamModelsResult
} from '@/api/admin/accounts'
import { areContextCapacityDraftsValid, buildContextOverridePatch } from '@/utils/modelContextCapacity'

const managedCapacityKeys = ['model_context_overrides', 'upstream_model_context_capacities', 'upstream_model_metadata']

function profileKey(params: ModelContextCapacitiesPreviewParams): string {
  return JSON.stringify([
    params.base_url ?? '', params.account_mode ?? '', params.api_protocol ?? '',
    Object.entries(params.api_base_urls ?? {}).sort(([a], [b]) => a.localeCompare(b))
  ])
}

/** Capacity observations and projections are server-owned, never written back through generic Extra. */
export function withoutManagedCapacityExtra(extra: Record<string, unknown>): Record<string, unknown> {
  const result = { ...extra }
  for (const key of managedCapacityKeys) delete result[key]
  return result
}

/** Modal-owned state survives selector unmounts and never mutates model restrictions. */
export function useModelContextCapacities(options: {
  enabled: () => boolean
  identity: () => string
  params: () => ModelContextCapacitiesPreviewParams
  syncIdentity?: () => string
}) {
  const rows = ref<ModelContextCapacityRow[]>([])
  const drafts = ref<Record<string, string>>({})
  const syncedModels = ref<SyncUpstreamModelsResult>()
  const syncedEvidence = ref<ModelContextCapacityRow[]>([])
  const invalidFields = ref(new Set<object | string>())
  const editingFields = ref(new Set<object | string>())
  const loading = ref(false)
  const loadFailed = ref(false)
  const syncing = ref(false)
  const initialProfile = ref('')
  const initialSyncIdentity = ref('')
  const profileChanged = computed(() => initialProfile.value !== profileKey(options.params()) ||
    initialSyncIdentity.value !== (options.syncIdentity?.() ?? ''))
  const syncSourceKey = computed(() => JSON.stringify([
    options.identity(), profileKey(options.params()), options.syncIdentity?.() ?? ''
  ]))
  const applicableDrafts = computed(() => Object.fromEntries(Object.entries(drafts.value)
    .filter(([id]) => rows.value.find(row => row.upstream_model_id === id)?.editable !== false)))
  const valid = computed(() => invalidFields.value.size === 0 && editingFields.value.size === 0 &&
    areContextCapacityDraftsValid(applicableDrafts.value))
  const ready = computed(() => {
    const touched = Object.keys(drafts.value)
    return touched.length === 0 || (!loading.value && !loadFailed.value &&
      touched.every(id => rows.value.some(row => row.upstream_model_id === id)))
  })
  let sequence = 0
  let syncSequence = 0
  let timer: ReturnType<typeof setTimeout> | undefined
  let request: AbortController | undefined

  const cancelRequest = () => {
    sequence += 1
    if (timer) clearTimeout(timer)
    timer = undefined
    request?.abort()
    request = undefined
  }

  const reset = () => {
    cancelRequest()
    syncSequence += 1
    syncing.value = false
    rows.value = []
    drafts.value = {}
    syncedModels.value = undefined
    syncedEvidence.value = []
    invalidFields.value.clear()
    editingFields.value.clear()
    loading.value = false
    loadFailed.value = false
  }

  const applyRows = (incoming: ModelContextCapacityRow[]) => {
    const observations = new Map(syncedEvidence.value.map(row => [row.upstream_model_id, row]))
    rows.value = incoming.map(row => {
      const observation = observations.get(row.upstream_model_id)
      if (!row.editable || row.upstream || !observation?.upstream) return row
      // Unsaved creation previews cannot persist the latest upstream observation.
      // Retain that actual sync result while asking the backend to resolve new aliases/products.
      if (row.automatic_source === 'default' && observation.automatic_source === 'upstream') {
        return {
          ...observation,
          ...row,
          upstream: observation.upstream,
          automatic_source: observation.automatic_source,
          automatic_context_window: observation.automatic_context_window,
          effective_source: row.custom_context_window ? 'custom' : observation.effective_source,
          effective_context_window: row.custom_context_window ?? observation.effective_context_window,
          capacity_basis: observation.capacity_basis,
          max_context_window: observation.max_context_window,
          max_input_tokens: observation.max_input_tokens,
          max_output_tokens: observation.max_output_tokens
        }
      }
      return { ...row, upstream: observation.upstream }
    })
  }

  const refresh = async (initial = false) => {
    cancelRequest()
    if (!options.enabled()) return
    const current = sequence
    const controller = new AbortController()
    request = controller
    loading.value = true
    loadFailed.value = false
    try {
      const params = options.params()
      const savedAccountId = initial ? params.account_id : undefined
      const result = savedAccountId
        ? await adminAPI.accounts.getModelContextCapacities(savedAccountId, controller.signal)
        : await adminAPI.accounts.previewModelContextCapacities({
          ...params,
          model_ids: [...new Set([...(params.model_ids ?? []), ...(syncedModels.value?.models ?? []), ...Object.keys(drafts.value)])]
        }, controller.signal)
      if (current !== sequence) return
      applyRows(result.capacity_rows ?? [])
      // The saved snapshot need not contain unselected built-in candidates or
      // draft compact targets. Populate those through the same local resolver.
      if (savedAccountId && params.model_ids?.some(id =>
        !rows.value.some(row => row.upstream_model_id === id))) {
        await refresh()
      }
    } catch {
      if (current === sequence) loadFailed.value = true
    } finally {
      if (current === sequence) loading.value = false
    }
  }

  const scheduleRefresh = () => {
    cancelRequest()
    loading.value = options.enabled()
    if (options.enabled()) timer = setTimeout(() => { void refresh() }, 250)
  }

  const acceptSync = (result?: SyncUpstreamModelsResult) => {
    if (!result || !options.enabled()) return
    cancelRequest()
    syncedModels.value = result
    loading.value = false
    loadFailed.value = false
    const savedAccount = Boolean(options.params().account_id)
    // Saved-account sync deliberately uses persisted credentials. Its model IDs
    // still belong in the selector, but a dirty form must not adopt capacity
    // evidence from that different endpoint/product/credential identity.
    syncedEvidence.value = savedAccount && profileChanged.value ? [] : result.capacity_rows ?? []
    if (!savedAccount && result.capacity_rows) {
      const merged = new Map(rows.value.map(row => [row.upstream_model_id, row]))
      for (const row of result.capacity_rows) merged.set(row.upstream_model_id, row)
      applyRows([...merged.values()])
    } else if (syncedEvidence.value.length > 0) {
      // Keep current unsaved aliases until the local projection returns; the
      // upstream sync response may contain the saved account's older mappings.
      applyRows(rows.value)
    }
    void refresh()
  }

  const synchronize = async (perform: () => Promise<SyncUpstreamModelsResult>) => {
    if (syncing.value || !options.enabled()) return undefined
    const current = ++syncSequence
    const sourceKey = JSON.stringify([options.identity(), options.params(), options.syncIdentity?.()])
    syncing.value = true
    try {
      const result = await perform()
      if (current !== syncSequence || !options.enabled() ||
        sourceKey !== JSON.stringify([options.identity(), options.params(), options.syncIdentity?.()])) return undefined
      acceptSync(result)
      return result
    } catch (error) {
      if (current !== syncSequence || !options.enabled()) return undefined
      throw error
    } finally {
      if (current === syncSequence) syncing.value = false
    }
  }

  watch(
    () => [options.enabled(), options.identity(), options.params(), options.syncIdentity?.() ?? ''] as const,
    ([enabled, identity], previous) => {
      if (!enabled) {
        reset()
        return
      }
      if (!previous || !previous[0] || identity !== previous[1]) {
        reset()
        initialProfile.value = profileKey(options.params())
        initialSyncIdentity.value = options.syncIdentity?.() ?? ''
        void refresh(true)
      } else {
        syncSequence += 1
        syncing.value = false
        if (profileKey(options.params()) !== profileKey(previous[2]) || (options.syncIdentity?.() ?? '') !== previous[3]) {
          // A draft endpoint/product change must not label an old provider's
          // observation as evidence for the new provider. Keep only the edit draft.
          syncedModels.value = undefined
          syncedEvidence.value = []
          rows.value = []
        }
        scheduleRefresh()
      }
    },
    { immediate: true, deep: true }
  )

  onScopeDispose(() => {
    cancelRequest()
    syncSequence += 1
  })

  const buildPatch = () => {
    if (!ready.value) throw new Error('Capacity metadata is not ready for the current account profile.')
    const patch = buildContextOverridePatch(applicableDrafts.value)
    const editable = new Set(rows.value.filter(row => row.editable).map(row => row.upstream_model_id))
    return Object.fromEntries(Object.entries(patch).filter(([id]) => editable.has(id)))
  }

  const setFieldValidity = (field: object | string, isValid: boolean) => {
    if (isValid) invalidFields.value.delete(field)
    else invalidFields.value.add(field)
  }
  const setFieldEditing = (field: object | string, isEditing: boolean) => {
    if (isEditing) editingFields.value.add(field)
    else editingFields.value.delete(field)
  }

  return {
    rows, drafts, syncedModels, loading, loadFailed, syncing, profileChanged, syncSourceKey,
    valid, ready, acceptSync, synchronize, refresh, reset, buildPatch, setFieldValidity, setFieldEditing
  }
}
