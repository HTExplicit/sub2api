import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import type { PluginContribution } from '@/api/admin/plugins'
import type { AccountCreateDefaultTarget, AccountCreateDefinitionV1, ProviderCreateRequestV1 } from '@sub2api/plugin-ui/account-create'
import { usePluginExtensions } from '@/stores/pluginExtensions'
import { createContributionAdmission } from '@/components/plugins/contributionAdmission'

export const accountCreateDefaultTargets: AccountCreateDefaultTarget[] = ['concurrency', 'priority', 'rate_multiplier', 'load_factor', 'responses_mode']
type CreateContribution = PluginContribution & { account_create: AccountCreateDefinitionV1; plugin_key: string; package_sha256: string; create_definition_digest: string; runtime_generation: number }
const digest = (value: unknown): value is string => typeof value === 'string' && /^[a-f0-9]{64}$/.test(value)
const copy = <T>(value: T): T => JSON.parse(JSON.stringify(value)) as T
const ownerKey = (item: PluginContribution) => `${item.plugin_key}:${item.id}`
const revision = (item: CreateContribution) => JSON.stringify([item.plugin_id, item.package_sha256, item.create_definition_digest, item.runtime_generation])

export function isAccountCreateProfile(item: PluginContribution): item is CreateContribution {
  const profile = item.account_create
  return item.slot === 'account.create.v1' && item.permission === 'admin' && item.capability === 'extensions.provider.v1' &&
    Number.isSafeInteger(item.plugin_id) && item.plugin_id > 0 && !!item.plugin_key && digest(item.package_sha256) && digest(item.create_definition_digest) &&
    Number.isSafeInteger(item.runtime_generation) && item.runtime_generation! > 0 && !!profile && profile.version === 1 &&
    profile.platform === 'cindy' && profile.account_type === 'apikey' && profile.credential_profile === 'cindy_laxa_v1' &&
    !!profile.credential_ui?.base_url && profile.credential_ui.base_url_readonly === true && profile.minimum_effective_groups === 1 &&
    profile.model_editing === 'provider_managed' && profile.catalog_source === 'group_model_candidates' && profile.upstream_billing_probe === 'unsupported' &&
    !!profile.defaults && accountCreateDefaultTargets.every(key => Object.prototype.hasOwnProperty.call(profile.defaults, key)) &&
    Number.isSafeInteger(profile.defaults.concurrency) && profile.defaults.concurrency >= 1 &&
    Number.isSafeInteger(profile.defaults.priority) && profile.defaults.priority >= 0 && Number.isFinite(profile.defaults.rate_multiplier) && profile.defaults.rate_multiplier >= 0 &&
    (profile.defaults.load_factor === null || (Number.isSafeInteger(profile.defaults.load_factor) && profile.defaults.load_factor >= 1 && profile.defaults.load_factor <= 10000)) &&
    ['auto', 'force_responses', 'force_chat_completions'].includes(profile.defaults.responses_mode) &&
    !!profile.field_bindings && Object.keys(profile.field_bindings).length <= 8 &&
    Object.entries(profile.field_bindings).every(([key, target]) => target === 'provider_device_identity_input' && item.fields?.some(field => field.key === key && field.kind === 'text'))
}

interface ProfileDraft {
  contribution: CreateContribution
  values: Record<string, string>
  explicit: Set<AccountCreateDefaultTarget>
  catalog: string[]
  catalogLoaded: boolean
  catalogLoading: boolean
  catalogFailed: boolean
}

/** Host-only state. Nothing here is persisted or exposed to a plugin frame. */
export function useAccountCreateProfile(options: {
  platform(): string
  accountType(): string
  baseURL(): string
  actorID(): number | undefined
  loadCatalog(platform: AccountCreateDefinitionV1['platform']): Promise<string[]>
}) {
  const registry = usePluginExtensions()
  const drafts = reactive(new Map<string, ProfileDraft>())
  const platformOwners = reactive(new Map<string, string>())
  let muted = 0, sequence = 0
  const stateVersion = ref(0)
  function currentForPlatform(platform: string) {
    const candidates = registry.items.filter(item => item.slot === 'account.create.v1' && item.account_create?.platform === platform)
    return candidates.length === 1 && isAccountCreateProfile(candidates[0]!) ? candidates[0] : undefined
  }
  const choices = computed(() => registry.items.filter(isAccountCreateProfile).filter(item => currentForPlatform(item.account_create.platform) === item))
  const draft = computed(() => drafts.get(platformOwners.get(options.platform()) || ''))
  const required = computed(() => options.platform() === 'cindy' || platformOwners.has(options.platform()))
  const contribution = computed(() => draft.value?.contribution)
  const definition = computed(() => contribution.value?.account_create)
  const current = computed(() => currentForPlatform(options.platform()))
  const stale = computed(() => !!contribution.value && !!current.value && revision(contribution.value) !== revision(current.value))
  const available = computed(() => {
    const old = contribution.value, next = current.value
    return !!old && !!next && !stale.value && old.plugin_key === next.plugin_key && old.id === next.id &&
      options.baseURL().trim() === old.account_create.credential_ui.base_url &&
      createContributionAdmission(next, { platform: options.platform(), accountType: options.accountType() }).allowed
  })
  const values = computed<Record<string, string>>({ get: () => draft.value?.values || {}, set: value => { if (draft.value) draft.value.values = { ...value } } })
  const fieldsValid = computed(() => !!contribution.value && (contribution.value.fields || []).every(field =>
    values.value[field.key] === undefined || (typeof values.value[field.key] === 'string' && [...values.value[field.key]].length <= (field.max_length || 0))))
  function activate(platform: string) {
    const candidate = currentForPlatform(platform)
    if (candidate && !platformOwners.has(platform)) {
      const key = ownerKey(candidate)
      if (!drafts.has(key)) drafts.set(key, { contribution: copy(candidate), values: {}, explicit: new Set(), catalog: [], catalogLoaded: false, catalogLoading: false, catalogFailed: false })
      platformOwners.set(platform, key)
    }
    return drafts.get(platformOwners.get(platform) || '')
  }
  function markExplicit(target: AccountCreateDefaultTarget) { if (!muted) draft.value?.explicit.add(target) }
  function preserveIntent<T>(write: () => T): T { muted++; try { return write() } finally { muted-- } }
  function reconcile() {
    const next = current.value, selected = draft.value
    if (!next || !selected || next.plugin_key !== selected.contribution.plugin_key || next.id !== selected.contribution.id ||
        !createContributionAdmission(next, { platform: next.account_create.platform, accountType: next.account_create.account_type }).allowed) return false
    selected.contribution = copy(next)
    selected.catalogLoaded = false
    stateVersion.value++
    return true
  }
  async function refreshCatalog() {
    const selected = draft.value, descriptor = contribution.value
    if (!selected || !descriptor || !available.value || selected.catalogLoading) return
    const request = ++sequence, identity = revision(descriptor), actor = options.actorID(), platform = options.platform(), type = options.accountType(), endpoint = options.baseURL()
    selected.catalogLoading = true; selected.catalogFailed = false
    const currentRequest = () => request === sequence && draft.value === selected && options.actorID() === actor && available.value &&
      revision(selected.contribution) === identity && options.platform() === platform && options.accountType() === type && options.baseURL() === endpoint
    try {
      const ids = await options.loadCatalog(descriptor.account_create.platform)
      if (!currentRequest()) return
      if (!Array.isArray(ids) || ids.length > 10000 || ids.some(id => typeof id !== 'string' || !id || id.length > 256)) throw new Error('Invalid provider model catalog')
      selected.catalog = [...new Set(ids)]
      selected.catalogLoaded = true
    } catch { if (currentRequest()) selected.catalogFailed = true }
    finally { if (request === sequence) selected.catalogLoading = false }
  }
  watch(() => [options.platform(), options.accountType(), options.baseURL(), available.value, contribution.value && revision(contribution.value), stateVersion.value], () => {
    sequence++
    for (const value of drafts.values()) value.catalogLoading = false
    if (available.value && draft.value && !draft.value.catalogLoaded) void refreshCatalog()
  }, { flush: 'post' })
  function request(): ProviderCreateRequestV1 {
    const selected = draft.value, item = contribution.value
    if (!selected || !item || !available.value || !fieldsValid.value) throw new Error('Provider create profile unavailable')
    const declared = Object.keys(item.account_create.field_bindings)
    return { contribution_id: item.id, expected_package_sha256: item.package_sha256,
      expected_definition_sha256: item.create_definition_digest, expected_runtime_generation: item.runtime_generation,
      values: Object.fromEntries(declared.filter(key => values.value[key] !== undefined).map(key => [key, values.value[key]!])),
      inherit_defaults: accountCreateDefaultTargets.filter(key => !selected.explicit.has(key)) }
  }
  function assertRequest(expected: ProviderCreateRequestV1) {
    const fresh = request()
    if (fresh.contribution_id !== expected.contribution_id || fresh.expected_package_sha256 !== expected.expected_package_sha256 ||
        fresh.expected_definition_sha256 !== expected.expected_definition_sha256 || fresh.expected_runtime_generation !== expected.expected_runtime_generation) throw new Error('Provider create profile changed')
  }
  function reset() { sequence++; drafts.clear(); platformOwners.clear(); stateVersion.value++ }
  watch(options.actorID, reset, { flush: 'sync' })
  onMounted(() => { if (!registry.loaded) void registry.refresh() })
  onBeforeUnmount(reset)
  return { choices, draft, required, contribution, definition, available, stale, values, fieldsValid,
    activate, markExplicit, preserveIntent, reconcile, refreshCatalog, request, assertRequest, reset }
}
