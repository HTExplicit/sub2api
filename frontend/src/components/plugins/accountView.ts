import type { PluginContribution } from '@/api/admin/plugins'
import type { AccountListFilters } from '@/api/admin/accounts'
import type { Account, AccountConsoleFacets } from '@/types'
import type {
  AccountViewContextV1, AccountViewDefinitionV1, AccountViewIdentityV1,
  AccountViewPredicate, AccountViewPreset, AccountViewQueryV1
} from '@sub2api/plugin-ui/account-view'

export const ACCOUNT_VIEW_HEADER = 'X-Sub2API-Account-View'
export const CINDY_ACCOUNT_VIEW_ALIAS = Object.freeze({
  path: '/admin/cindy-accounts', plugin_key: 'codexrip.cindy-provider', view_id: 'cindy-accounts',
  query_keys: ['cindy_only', 'cindy_balance_status', 'cindy_health_status'] as const
})

export interface ContributionRef { pluginKey?: string; pluginId?: number; packageSHA?: string; id: string; slot?: string }

/** Ambiguity is an error, never an array-order based ownership decision. */
export function resolveContribution(items: PluginContribution[], ref: ContributionRef): PluginContribution | undefined {
  const matches = items.filter(item => item.id === ref.id && (!ref.slot || item.slot === ref.slot) &&
    (!ref.pluginKey || item.plugin_key === ref.pluginKey) &&
    (ref.pluginId === undefined || item.plugin_id === ref.pluginId) &&
    (!ref.packageSHA || item.package_sha256 === ref.packageSHA))
  return matches.length === 1 ? matches[0] : undefined
}

export function isAccountView(item: PluginContribution | undefined): item is PluginContribution & { account_view: AccountViewDefinitionV1; plugin_key: string; package_sha256: string; view_definition_digest: string } {
  if (!item || item.slot !== 'account.view.v1' || item.permission !== 'admin' || !item.plugin_key ||
      !/^[a-f0-9]{64}$/.test(item.package_sha256 || '') || !/^[a-f0-9]{64}$/.test(item.view_definition_digest || '')) return false
  const view = item.account_view
  return !!view && view.version === 1 && view.source === 'accounts.console.v1' && Array.isArray(view.presets) &&
    view.presets.length > 0 && new Set(view.presets.map(preset => preset.id)).size === view.presets.length &&
    view.presets.some(preset => preset.id === view.default_preset) &&
    Array.isArray(view.layout) && view.layout.filter(node => node.kind === 'account_table').length === 1 &&
    !!view.table && Array.isArray(view.table.column_refs) && Array.isArray(view.table.action_refs)
}

export function resolveAccountView(items: PluginContribution[], pluginKey: string, viewID: string) {
  const item = resolveContribution(items, { pluginKey, id: viewID, slot: 'account.view.v1' })
  return isAccountView(item) ? item : undefined
}

export function ownedViewReference(items: PluginContribution[], owner: PluginContribution, id: string, slot: string) {
  // A descriptor never follows a reference into another owner, even when the
  // other package uses the same contribution ID.
  if (!owner.plugin_key || !owner.package_sha256) return undefined
  return resolveContribution(items, { pluginKey: owner.plugin_key, pluginId: owner.plugin_id,
    packageSHA: owner.package_sha256, id, slot })
}

export function accountViewPath(item: PluginContribution) {
  if (item.plugin_key === CINDY_ACCOUNT_VIEW_ALIAS.plugin_key && item.id === CINDY_ACCOUNT_VIEW_ALIAS.view_id) return CINDY_ACCOUNT_VIEW_ALIAS.path
  return `/admin/account-views/${encodeURIComponent(item.plugin_key || '')}/${encodeURIComponent(item.id)}`
}

export function hasLegacyAccountViewQuery(query: Record<string, unknown>) {
  return CINDY_ACCOUNT_VIEW_ALIAS.query_keys.some(key => query[key] !== undefined && query[key] !== '')
}

export function legacyAccountViewPreset(view: AccountViewDefinitionV1, query: Record<string, unknown>): string | undefined {
  const aliases = [...(view.legacy_query_aliases || [])].sort((a, b) => b.priority - a.priority)
  return aliases.find(alias => Object.entries(alias.match).every(([key, value]) => query[key] === value))?.preset
}

export function presetRouteQuery(view: AccountViewDefinitionV1, presetID: string): Record<string, string> {
  const alias = [...(view.legacy_query_aliases || [])].sort((a, b) => b.priority - a.priority).find(item => item.preset === presetID)
  const baseAlias = view.legacy_query_aliases?.find(item => item.preset === view.default_preset)
  return alias ? { ...baseAlias?.match, ...alias.match } : { view_preset: presetID }
}

export function viewIdentity(item: PluginContribution, presetID: string): AccountViewIdentityV1 {
  if (!isAccountView(item) || !item.account_view.presets.some(preset => preset.id === presetID) ||
      !Number.isSafeInteger(item.plugin_id) || item.plugin_id <= 0) throw new Error('Account view unavailable')
  return { version: 1, plugin_id: item.plugin_id, plugin_key: item.plugin_key, package_sha256: item.package_sha256,
    view_id: item.id, preset_id: presetID, view_definition_digest: item.view_definition_digest }
}

export function accountViewIdentityKey(identity: AccountViewIdentityV1) {
  return JSON.stringify([identity.version, identity.plugin_id, identity.plugin_key, identity.package_sha256,
    identity.view_id, identity.preset_id, identity.view_definition_digest])
}

export function accountViewIdentityHeader(identity: AccountViewIdentityV1) {
  const bytes = new TextEncoder().encode(JSON.stringify(identity))
  return btoa(Array.from(bytes, byte => String.fromCharCode(byte)).join('')).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

function values(value: string | string[] | undefined) {
  return [...new Set((Array.isArray(value) ? value : (value || '').split(',')).map(item => item.trim()).filter(Boolean))].sort()
}
function ids(value: string | undefined) {
  const result = values(value).map(Number)
  if (result.some(id => !Number.isSafeInteger(id) || id <= 0)) throw new Error('Invalid account view selection')
  return result.sort((a, b) => a - b)
}

export function accountViewQuery(filters: AccountListFilters): AccountViewQueryV1 {
  const query: AccountViewQueryV1 = {}
  for (const key of ['platforms', 'types', 'statuses', 'plans', 'proxies'] as const) {
    const result = values(filters[key]); if (result.length) query[key] = result
  }
  const folders = values(filters.folders || filters.folder)
  if (folders.length) query.folders = folders
  for (const key of ['tags', 'account_ids'] as const) {
    const result = ids(filters[key]); if (result.length) query[key] = result
  }
  if (filters.group_id) {
    const group = filters.group_id === 'ungrouped' ? -1 : Number(filters.group_id)
    if (!Number.isSafeInteger(group) || group < -1) throw new Error('Invalid account view group')
    if (group) query.group_id = group
  }
  if (filters.privacy_mode) query.privacy_mode = filters.privacy_mode
  if (filters.search?.trim()) query.search = filters.search.trim()
  if (filters.sort_by) query.sort_by = filters.sort_by
  if (filters.sort_order) query.sort_order = filters.sort_order
  return query
}

export function viewPredicateParams(view: AccountViewDefinitionV1, presetID: string): AccountListFilters {
  const preset = view.presets.find(item => item.id === presetID)
  if (!preset) throw new Error('Account view preset unavailable')
  // The server re-resolves both layers. These parameters preserve the existing
  // HTTP/filter display contract, not the authorization boundary.
  const predicate = { ...view.base_query, ...preset.query }
  const result: AccountListFilters = {}
  for (const key of ['platforms', 'types', 'statuses', 'plans'] as const) if (predicate[key]?.length) result[key] = predicate[key]!.join(',')
  if (predicate.privacy_mode) result.privacy_mode = predicate.privacy_mode
  if (predicate.cindy_only !== undefined) result.cindy_only = String(predicate.cindy_only)
  if (predicate.cindy_balance_status) result.cindy_balance_status = predicate.cindy_balance_status
  if (predicate.cindy_health_status) result.cindy_health_status = predicate.cindy_health_status
  return result
}

export function accountViewPresetCount(facets: AccountConsoleFacets | null, preset: AccountViewPreset): number | undefined {
  // Never fall back to unscoped legacy counters on a view-bound response.
  const count = facets?.view_preset_counts?.[preset.id]
  return typeof count === 'number' && Number.isFinite(count) && count >= 0 && preset.counter ? count : undefined
}

export function localizedPluginLabel(labels: Record<string, string>, locale: string) {
  return labels[locale] || labels[locale.startsWith('zh') ? 'zh' : 'en'] || labels.en || labels.zh || ''
}

const ACCOUNT_DISPLAY_FIELDS = new Set([
  'id', 'name', 'platform', 'type', 'status', 'cindy_balance_probe_job_id',
  'cindy_balance_probe_outcome', 'cindy_balance_probe_checked_at'
])

export function accountDisplayValues(item: PluginContribution, account: Account): Record<string, string | number | null> {
  const result: Record<string, string | number | null> = {}
  for (const [key, source] of Object.entries(item.value_bindings || {})) {
    if (!ACCOUNT_DISPLAY_FIELDS.has(source) || !item.display_fields?.some(field => field.key === key)) continue
    const value = account[source as keyof Account]
    if (value === null || typeof value === 'string' || (typeof value === 'number' && Number.isFinite(value))) result[key] = value
  }
  return result
}

export function accountMatchesPredicate(account: Account, predicate?: AccountViewPredicate) {
  if (!predicate) return true
  const keys = new Set(['platforms', 'types', 'statuses', 'plans', 'privacy_mode', 'cindy_only', 'cindy_balance_status', 'cindy_health_status'])
  if (Object.keys(predicate).some(key => !keys.has(key))) return false
  if (predicate.privacy_mode !== undefined && typeof predicate.privacy_mode !== 'string') return false
  if (predicate.cindy_only !== undefined && typeof predicate.cindy_only !== 'boolean') return false
  if (predicate.cindy_balance_status !== undefined && predicate.cindy_balance_status !== 'insufficient') return false
  if (predicate.cindy_health_status !== undefined && predicate.cindy_health_status !== 'banned') return false
  for (const key of ['platforms', 'types', 'statuses', 'plans'] as const) {
    if (predicate[key] !== undefined && (!Array.isArray(predicate[key]) || predicate[key]!.some(value => typeof value !== 'string'))) return false
  }
  if (predicate.platforms?.length && !predicate.platforms.includes(account.platform)) return false
  if (predicate.types?.length && !predicate.types.includes(account.type)) return false
  const needsFacts = !!(predicate.statuses?.length || predicate.plans?.length || predicate.privacy_mode ||
    predicate.cindy_only !== undefined || predicate.cindy_balance_status || predicate.cindy_health_status)
  if (!needsFacts) return true
  // The same server helper evaluates these facts for source filtering and row
  // admission. Never infer canonical identity from a URL, copy private provider
  // logic, or treat an absent/unknown fact version as an unconstrained match.
  const facts = account.account_view_facts
  if (facts?.version !== 1 || typeof facts.status !== 'string' || typeof facts.plan !== 'string' || typeof facts.privacy_mode !== 'string' ||
      typeof facts.canonical_cindy !== 'boolean' || typeof facts.cindy_balance_insufficient !== 'boolean' || typeof facts.cindy_banned !== 'boolean') return false
  if (predicate.statuses?.length && !predicate.statuses.includes(facts.status)) return false
  if (predicate.plans?.length && !predicate.plans.some(plan => plan.toLowerCase() === facts.plan.toLowerCase())) return false
  if (predicate.privacy_mode && facts.privacy_mode !== (predicate.privacy_mode === '__unset__' ? '' : predicate.privacy_mode)) return false
  if (predicate.cindy_only !== undefined && predicate.cindy_only !== facts.canonical_cindy) return false
  if (predicate.cindy_balance_status !== undefined && (predicate.cindy_balance_status !== 'insufficient' || !facts.canonical_cindy || !facts.cindy_balance_insufficient)) return false
  if (predicate.cindy_health_status !== undefined && (predicate.cindy_health_status !== 'banned' || !facts.canonical_cindy || !facts.cindy_banned)) return false
  return true
}

export function accountColumnKey(item: PluginContribution) { return `extension_${item.plugin_id}_${item.id}` }

/** Other table-slot owners keep independent admission; owned actions are declared by the view. */
export function accountViewAllowsAction(item: PluginContribution, owner?: PluginContribution) {
  if (!owner || item.plugin_id !== owner.plugin_id) return true
  return isAccountView(owner) && item.plugin_key === owner.plugin_key && item.package_sha256 === owner.package_sha256 &&
    owner.account_view.table.action_refs.includes(item.id)
}

export { publicPluginContext } from './pluginContext'

export function cloneViewContext(context: AccountViewContextV1): AccountViewContextV1 {
  return JSON.parse(JSON.stringify(context)) as AccountViewContextV1
}
