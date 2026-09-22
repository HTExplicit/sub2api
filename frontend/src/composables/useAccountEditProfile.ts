import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import type { PluginContribution } from '@/api/admin/plugins'
import type { Account, AccountEditContext, ProviderAccountEditContext, UpdateAccountRequest } from '@/types'
import type { AccountEditChangesV1, AccountEditDefinitionV1, AccountEditModeTarget, AccountEditTarget, ProviderEditRequestV1 } from '@sub2api/plugin-ui/account-edit'
import { usePluginExtensions } from '@/stores/pluginExtensions'
import { contributionAdmission } from '@/components/plugins/contributionAdmission'
import { normalizeOpenAIWSMode } from '@/utils/openaiWsMode'
import {
  accountEditModes, captureAccountEditState, copyAccountEditValue, effectiveAccountEditChanges,
  mappingInputFromStored, partitionAccountEditMappings, prepareAccountProviderEdit, projectAccountEditCatalog,
  requiresAccountEditProfile, type AccountEditInput, type AccountEditStoredState, type ReadyAccountEditCatalog
} from '@/utils/accountEditCodec'

export type AccountEditContribution = PluginContribution & {
  account_edit: AccountEditDefinitionV1; plugin_key: string; package_sha256: string
  edit_definition_digest: string; runtime_generation: number
}
const digest = (value: unknown): value is string => typeof value === 'string' && /^[a-f0-9]{64}$/.test(value)
const revision = (item: AccountEditContribution) => JSON.stringify([item.plugin_id, item.plugin_key, item.id, item.package_sha256, item.edit_definition_digest, item.runtime_generation])
const sameOwner = (a: AccountEditContribution, b: AccountEditContribution) => a.plugin_id === b.plugin_id && a.plugin_key === b.plugin_key && a.id === b.id

export function isAccountEditProfile(item: PluginContribution): item is AccountEditContribution {
  const definition = item.account_edit
  if (item.slot !== 'account.edit.v1' || item.permission !== 'admin' || item.capability !== 'extensions.provider.v1' ||
      !Number.isSafeInteger(item.plugin_id) || item.plugin_id <= 0 || !item.plugin_key || !digest(item.package_sha256) || !digest(item.edit_definition_digest) ||
      !Number.isSafeInteger(item.runtime_generation) || item.runtime_generation! <= 0 || item.account_scope?.version !== 1 ||
      item.entrypoint || item.action || item.all_accounts || item.retained_controls || item.account_filter ||
      !definition || definition.version !== 1 || definition.platform !== 'cindy' || definition.account_type !== 'apikey' || definition.credential_profile !== 'cindy_laxa_v1' ||
      !definition.credential_ui?.base_url || definition.credential_ui.base_url_readonly !== true || definition.catalog_source !== 'provider_catalog_snapshot_v1' ||
      definition.mapping_policy !== 'managed_catalog_v1' || definition.unlisted_fields !== 'preserve' || !Array.isArray(definition.wire_controls) || definition.wire_controls.length > 3) return false
  const seen = new Set<string>()
  return definition.wire_controls.every(control => {
    const mode = accountEditModes[control.target]
    if (!mode || seen.has(control.target) || !Array.isArray(control.values) || !control.values.length || typeof control.allow_clear !== 'boolean') return false
    seen.add(control.target)
    return new Set(control.values).size === control.values.length && control.values.every(value => (mode.values as readonly string[]).includes(value))
  })
}

interface EditDraft {
  accountID: number
  actorID: number | undefined
  input: AccountEditInput
  stored: AccountEditStoredState
  stateSHA256?: string
  intent: AccountEditChangesV1
  preserved: Record<string, string>
  contribution?: AccountEditContribution
  context?: ProviderAccountEditContext
  pendingContext?: ProviderAccountEditContext
  catalog?: ReadyAccountEditCatalog
  catalogNeedsReview: boolean
  loading: boolean
  failed: boolean
  reason?: string
}

/** Host-owned, in-memory drafts; no credentials or native component implementation reach a plugin. */
export function useAccountEditProfile(options: {
  active(): boolean
  account(): Account | null
  actorID(): number | undefined
  readInput(): AccountEditInput
  writeInput(input: AccountEditInput): void
  loadContext(id: number, signal: AbortSignal): Promise<AccountEditContext>
}) {
  const registry = usePluginExtensions()
  const drafts = reactive(new Map<string, EditDraft>())
  const selectedKey = ref('')
  const draft = computed(() => drafts.get(selectedKey.value))
  const required = computed(() => requiresAccountEditProfile(options.account()))
  let sequence = 0, muted = 0
  let controller: AbortController | undefined
  const dirty = computed(() => !!draft.value && Object.keys(draft.value.intent).length > 0)
  const contribution = computed(() => draft.value?.contribution)
  const definition = computed(() => contribution.value?.account_edit)
  const candidate = computed(() => {
    const account = options.account()
    if (!required.value || !account) return undefined
    const items = registry.items.filter(item => item.slot === 'account.edit.v1' && item.account_edit?.platform === account.platform && item.account_edit.account_type === account.type)
    return items.length === 1 && isAccountEditProfile(items[0]!) ? items[0] : undefined
  })
  const matches = (context: ProviderAccountEditContext, item: AccountEditContribution) => {
    const profile = context.profile
    return profile.plugin_id === item.plugin_id && profile.plugin_key === item.plugin_key && profile.contribution_id === item.id &&
      profile.package_sha256 === item.package_sha256 && profile.definition_sha256 === item.edit_definition_digest && profile.runtime_generation === item.runtime_generation
  }
  const pairedState = computed(() => {
    const selected = draft.value
    return !!selected?.context && digest(selected.stateSHA256) && selected.stateSHA256 === selected.context.edit_state_sha256 &&
      selected.stateSHA256 === options.account()?.account_edit_state_sha256
  })
  const available = computed(() => {
    const selected = draft.value, next = candidate.value, account = options.account()
    return pairedState.value && !!selected?.context && !!selected.contribution && !!next && !!account && !!options.actorID() && !selected.failed && !selected.pendingContext &&
      selected.context.profile.available && revision(next) === revision(selected.contribution) && matches(selected.context, next) &&
      contributionAdmission(next, { account }).allowed
  })
  const catalogAvailable = computed(() => available.value && !draft.value?.catalogNeedsReview && draft.value?.context?.catalog.status === 'ready' &&
    draft.value.context.catalog.namespace === draft.value.catalog?.namespace)
  const reason = computed(() => draft.value?.reason || candidate.value?.reason || (available.value ? undefined : 'account_edit_unavailable'))
  const projection = computed(() => draft.value?.catalog ? projectAccountEditCatalog(draft.value.catalog) : { models: [], aliases: [] })
  const changes = computed<AccountEditChangesV1>(() => {
    const selected = draft.value
    if (!selected) return {}
    // A new context digest cannot prove a no-op against an older native raw snapshot.
    if (!pairedState.value) return copyAccountEditValue(selected.intent)
    return effectiveAccountEditChanges(selected.stored, selected.input, selected.intent,
      catalogAvailable.value ? selected.catalog : undefined, selected.preserved)
  })
  const pending = computed(() => Object.keys(changes.value).length > 0)
  const canSubmit = computed(() => !pending.value || (available.value && (!changes.value.model_mapping || catalogAvailable.value)))
  function preserveIntent<T>(write: () => T): T { muted++; try { return write() } finally { muted-- } }
  function writeDraft(selected: EditDraft) { preserveIntent(() => options.writeInput(copyAccountEditValue(selected.input))) }
  function cancelRead() { sequence++; controller?.abort(); controller = undefined; for (const item of drafts.values()) item.loading = false }
  function activate(account: Account) {
    cancelRead()
    if (!requiresAccountEditProfile(account)) { selectedKey.value = ''; return }
    const key = JSON.stringify([options.actorID(), account.id, account.platform, account.type])
    selectedKey.value = key
    if (!drafts.has(key)) drafts.set(key, {
      accountID: account.id, actorID: options.actorID(), input: copyAccountEditValue(options.readInput()),
      stored: captureAccountEditState(account), stateSHA256: account.account_edit_state_sha256,
      intent: {}, preserved: {}, contribution: candidate.value && copyAccountEditValue(candidate.value),
      catalogNeedsReview: false, loading: false, failed: false
    })
    else writeDraft(drafts.get(key)!)
    // The modal can mount closed before any plugin surface has loaded the registry.
    if (!registry.loaded) void registry.refresh()
    void refresh()
  }
  function updateInput(target: AccountEditTarget, value?: string) {
    const selected = draft.value
    if (muted || !selected) return
    selected.input = copyAccountEditValue(options.readInput())
    if (target in accountEditModes) {
      const mode = target as AccountEditModeTarget
      if (!(accountEditModes[mode].values as readonly string[]).includes(value || '')) throw new Error('Invalid account edit mode')
      selected.intent = { ...selected.intent, [mode]: { op: 'set', value } }
    } else if (target === 'model_mapping' || target === 'compact_model_mapping') selected.intent[target] = { op: 'set' }
  }
  function clear(target: AccountEditTarget) {
    const selected = draft.value
    if (!selected) return
    if (target in accountEditModes && !definition.value?.wire_controls.find(control => control.target === target)?.allow_clear) return
    selected.input = copyAccountEditValue(options.readInput())
    selected.intent = { ...selected.intent, [target]: { op: 'clear' } }
    if (target === 'model_mapping') selected.input.model_mapping = { mode: selected.input.model_mapping.mode, allowed: [], rows: [] }
    else if (target === 'compact_model_mapping') selected.input.compact_model_mapping = []
    writeDraft(selected)
  }
  function validateContext(context: AccountEditContext, id: number): asserts context is ProviderAccountEditContext {
    if (!context || context.schema_version !== 1 || context.account_id !== id || context.kind !== 'provider' || !digest(context.edit_state_sha256) ||
        !context.profile || typeof context.profile.available !== 'boolean' || !context.values || !context.catalog) throw new Error('Invalid account edit context')
    if (context.catalog.status === 'ready') {
      const catalog = context.catalog
      if (typeof catalog.namespace !== 'string' || !catalog.namespace || catalog.namespace.length > 1024 || !Array.isArray(catalog.models) || catalog.models.length > 10000 ||
          catalog.models.some(model => !model || typeof model.id !== 'string' || !model.id || model.id.length > 256) ||
          !catalog.aliases || typeof catalog.aliases !== 'object' || Array.isArray(catalog.aliases) || Object.keys(catalog.aliases).length > 10000 ||
          Object.entries(catalog.aliases).some(([id, target]) => !id || id.length > 256 || typeof target !== 'string' || !target || target.length > 256)) throw new Error('Invalid account edit catalog')
    } else if (context.catalog.status !== 'unavailable' || typeof context.catalog.reason !== 'string') throw new Error('Invalid account edit catalog state')
    for (const target of Object.keys(accountEditModes) as AccountEditModeTarget[]) {
      const field = context.values[target]
      const mode = target === 'responses_websocket_mode' ? normalizeOpenAIWSMode(field?.effective) : field?.effective
      if (!field || typeof field.present !== 'boolean' || typeof field.recognized !== 'boolean' || !(accountEditModes[target].values as readonly string[]).includes(mode || '')) throw new Error('Invalid account edit field state')
    }
  }
  function acceptContext(selected: EditDraft, context: ProviderAccountEditContext, item?: AccountEditContribution, reconcile = false) {
    if (item && (!selected.contribution || sameOwner(selected.contribution, item))) selected.contribution = copyAccountEditValue(item)
    selected.context = copyAccountEditValue(context)
    selected.pendingContext = undefined
    selected.reason = context.profile.reason
    if (!dirty.value && options.account()) {
      selected.stored = captureAccountEditState(options.account()!)
      selected.stateSHA256 = options.account()!.account_edit_state_sha256
    }
    for (const target of Object.keys(accountEditModes) as AccountEditModeTarget[]) {
      if (selected.intent[target]) continue
      const value = context.values[target].effective
      if (target === 'responses_mode') selected.input.responses_mode = value as AccountEditInput['responses_mode']
      else if (target === 'compact_mode') selected.input.compact_mode = value as AccountEditInput['compact_mode']
      else selected.input.responses_websocket_mode = normalizeOpenAIWSMode(value)!
    }
    if (context.catalog.status === 'ready' && context.profile.available && item && matches(context, item)) {
      const changed = !!selected.catalog && selected.catalog.namespace !== context.catalog.namespace
      if (selected.intent.model_mapping && !reconcile && (!selected.catalog || changed)) selected.catalogNeedsReview = true
      else {
        selected.catalog = copyAccountEditValue(context.catalog)
        selected.catalogNeedsReview = false
        // Never partition live dirty rows. A reconciled dirty draft keeps its old
        // preserved pairs and editable input; the server merges against its fresh snapshot.
        if (!selected.intent.model_mapping) {
          const partition = partitionAccountEditMappings(selected.stored, selected.catalog)
          selected.preserved = partition.managed
          selected.input.model_mapping = mappingInputFromStored(partition.editable)
        }
      }
    }
    writeDraft(selected)
  }
  async function refresh() {
    const selected = draft.value
    if (!selected || !options.active() || !required.value) return
    cancelRead()
    const request = sequence, actor = options.actorID(), id = selected.accountID, registryRevision = candidate.value && revision(candidate.value)
    controller = new AbortController()
    selected.loading = true; selected.failed = false
    const current = () => request === sequence && options.active() && options.actorID() === actor && draft.value === selected && options.account()?.id === id &&
      (candidate.value && revision(candidate.value)) === registryRevision
    try {
      const context = await options.loadContext(id, controller.signal)
      if (!current()) return
      validateContext(context, id)
      const next = candidate.value
      const ownerChanged = selected.contribution && next && !sameOwner(selected.contribution, next)
      const stateChanged = selected.context && context.edit_state_sha256 !== selected.context.edit_state_sha256
      const definitionChanged = selected.contribution && next && revision(selected.contribution) !== revision(next)
      const nativeDigest = options.account()?.account_edit_state_sha256
      const baselineDigest = dirty.value ? selected.stateSHA256 : nativeDigest
      const unpaired = !digest(baselineDigest) || baselineDigest !== nativeDigest || baselineDigest !== context.edit_state_sha256
      if (ownerChanged || unpaired || (dirty.value && (stateChanged || definitionChanged))) {
        selected.pendingContext = copyAccountEditValue(context)
        selected.reason = ownerChanged ? 'account_edit_owner_changed' : unpaired || stateChanged ? 'account_edit_state_changed' : 'account_edit_definition_changed'
      } else acceptContext(selected, context, next)
    } catch {
      if (current()) { selected.failed = true; selected.reason = 'account_edit_context_unavailable' }
    } finally { if (request === sequence) selected.loading = false }
  }
  function reconcile() {
    const selected = draft.value, next = candidate.value, context = selected?.pendingContext || selected?.context
    if (!selected || !next || !context || !context.profile.available || !matches(context, next) ||
        (selected.contribution && !sameOwner(selected.contribution, next))) return false
    if (!digest(selected.stateSHA256) || selected.stateSHA256 !== options.account()?.account_edit_state_sha256 || selected.stateSHA256 !== context.edit_state_sha256) return false
    // A new stored state cannot be approved merely by refreshing a digest over an old draft.
    if (selected.context && selected.context.edit_state_sha256 !== context.edit_state_sha256) return false
    acceptContext(selected, context, next, true)
    return true
  }
  function request(): ProviderEditRequestV1 | undefined {
    const selected = draft.value, item = contribution.value
    if (!pending.value) return undefined
    if (!selected?.context || !item || !canSubmit.value) throw new Error('Provider edit is unavailable; save basic fields separately or reconcile the draft')
    for (const target of Object.keys(changes.value) as AccountEditTarget[]) {
      if (target === 'compact_model_mapping' && !item.account_edit.compact_mapping_editable) throw new Error('Compact mapping edit unavailable')
      if (target in accountEditModes) {
        const control = item.account_edit.wire_controls.find(control => control.target === target), change = changes.value[target as AccountEditModeTarget]!
        if (!control || (change.op === 'clear' ? !control.allow_clear : !(control.values as string[]).includes(change.value))) throw new Error('Mode edit unavailable')
      }
    }
    return {
      contribution_id: item.id, expected_package_sha256: item.package_sha256, expected_definition_sha256: item.edit_definition_digest,
      expected_runtime_generation: item.runtime_generation, expected_state_sha256: selected.context.edit_state_sha256,
      ...(changes.value.model_mapping ? { expected_catalog_namespace: selected.catalog!.namespace } : {}), changes: copyAccountEditValue(changes.value)
    }
  }
  function prepare(payload: UpdateAccountRequest) {
    const selected = draft.value
    if (!required.value || !selected) return
    const intent = request()
    prepareAccountProviderEdit(payload, selected.input, intent?.changes || {}, selected.preserved)
    if (intent) payload.provider_edit = intent
    else delete payload.provider_edit
  }
  function assertRequest(expected?: ProviderEditRequestV1) {
    if (expected && JSON.stringify(request()) !== JSON.stringify(expected)) throw new Error('Provider edit context changed')
  }
  function resetCurrent() { cancelRead(); drafts.delete(selectedKey.value); selectedKey.value = '' }
  function reset() { cancelRead(); drafts.clear(); selectedKey.value = '' }
  watch(options.actorID, reset, { flush: 'sync' })
  watch(() => JSON.stringify([candidate.value && revision(candidate.value), candidate.value?.available, candidate.value?.account_scope, registry.loaded]), () => { if (draft.value) void refresh() }, { flush: 'post' })
  onMounted(() => { if (required.value && !registry.loaded) void registry.refresh() })
  onBeforeUnmount(reset)
  return { draft, required, contribution, definition, available, catalogAvailable, reason, projection, pending, canSubmit,
    changes, preserveIntent, activate, updateInput, clear, refresh, reconcile, prepare, assertRequest, resetCurrent, cancelRead }
}
