// Explicit DTO projections for every current host context producer. This is not
// a recursive object sanitizer: an Account under any alias is never forwarded.
type ObjectValue = Record<string, unknown>
const object = (value: unknown): ObjectValue => value && typeof value === 'object' && !Array.isArray(value) ? value as ObjectValue : {}
function string(value: unknown, limit = 256) {
  if (value === undefined || value === null) return undefined
  if (typeof value !== 'string' || [...value].length > limit) throw new Error('Invalid plugin context string')
  return value
}
function number(value: unknown, positive = false) {
  if (value === undefined || value === null) return undefined
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || (positive ? value <= 0 : value < 0)) throw new Error('Invalid plugin context number')
  return value
}
function boolean(value: unknown) {
  if (value === undefined) return undefined
  if (typeof value !== 'boolean') throw new Error('Invalid plugin context boolean')
  return value
}
function array(value: unknown, item: (value: unknown) => unknown, limit = 3200) {
  if (value === undefined || value === null) return undefined
  if (!Array.isArray(value) || value.length > limit) throw new Error('Invalid plugin context list')
  return value.map(value => {
    const result = item(value)
    if (result === undefined) throw new Error('Invalid plugin context list entry')
    return result
  })
}
const ids = (value: unknown) => array(value, item => {
  const id = number(item, true)
  if (id === undefined) throw new Error('Invalid plugin context identifier')
  return id
})
const clean = (value: ObjectValue) => Object.fromEntries(Object.entries(value).filter(([, field]) => field !== undefined))

function labels(value: unknown) {
  const fields = Object.entries(object(value))
  if (fields.length > 16) throw new Error('Invalid plugin labels')
  return Object.fromEntries(fields.map(([key, label]) => [string(key, 32)!, string(label, 256)]))
}

function predicate(value: unknown) {
  const source = object(value)
  const result: ObjectValue = {}
  for (const key of ['platforms', 'types', 'statuses', 'plans']) result[key] = array(source[key], item => string(item, 64), 32)
  result.privacy_mode = string(source.privacy_mode, 64)
  result.cindy_only = boolean(source.cindy_only)
  for (const [key, allowed] of [['cindy_balance_status', 'insufficient'], ['cindy_health_status', 'banned']] as const) {
    if (source[key] !== undefined && source[key] !== allowed) throw new Error('Invalid plugin predicate')
    result[key] = source[key]
  }
  return clean(result)
}

function query(value: unknown) {
  const source = object(value), result: ObjectValue = {}
  for (const key of ['platforms', 'types', 'statuses', 'plans']) result[key] = array(source[key], item => string(item, 64), 32)
  for (const key of ['proxies', 'folders']) result[key] = array(source[key], item => string(item, 64))
  for (const key of ['tags', 'account_ids']) result[key] = ids(source[key])
  if (source.group_id !== undefined) {
    if (!Number.isSafeInteger(source.group_id) || (source.group_id as number) < -1) throw new Error('Invalid plugin context group')
    result.group_id = source.group_id
  }
  for (const key of ['privacy_mode', 'sort_by', 'sort_order']) result[key] = string(source[key], 64)
  const search = string(source.search, 100)
  if (search !== undefined && new TextEncoder().encode(search).length > 100) throw new Error('Invalid plugin context search')
  result.search = search
  return clean(result)
}

function viewState(value: unknown) {
  const source = object(value), identity = object(source.identity), preset = object(source.preset)
  if (identity.version !== 1 || !identity.plugin_key || !identity.view_id || !identity.preset_id || preset.id !== identity.preset_id) throw new Error('Invalid account view context')
  if (number(identity.plugin_id, true) === undefined) throw new Error('Invalid account view owner')
  for (const field of ['package_sha256', 'view_definition_digest']) if (typeof identity[field] !== 'string' || !/^[a-f0-9]{64}$/.test(identity[field] as string)) throw new Error('Invalid account view revision')
  const counts = Object.entries(object(source.preset_counts))
  if (counts.length > 16) throw new Error('Invalid account view counters')
  return clean({
    identity: { version: 1, plugin_id: number(identity.plugin_id, true), plugin_key: string(identity.plugin_key, 128),
      package_sha256: identity.package_sha256, view_id: string(identity.view_id, 64), preset_id: string(identity.preset_id, 64), view_definition_digest: identity.view_definition_digest },
    base_query: predicate(source.base_query),
    preset: { id: string(preset.id, 64), label: labels(preset.label), query: predicate(preset.query), counter: string(preset.counter, 64) },
    query: query(source.query), selected_ids: ids(source.selected_ids) || [],
    preset_counts: Object.fromEntries(counts.map(([key, count]) => [string(key, 64)!, number(count)])),
    available: boolean(source.available)
  })
}

/** Existing core filter and probe DTOs use CSV strings and finite scalar arrays. */
function filters(value: unknown) {
  const source = object(value), result: ObjectValue = {}
  for (const key of ['platform', 'type', 'status', 'platforms', 'types', 'statuses', 'plans', 'proxies', 'folder', 'folders', 'tags', 'account_ids', 'group_id', 'privacy_mode', 'search', 'sort_by', 'sort_order', 'cindy_only', 'cindy_balance_status', 'cindy_health_status']) {
    const field = source[key]
    if (field === undefined || field === null) continue
    if (Array.isArray(field)) result[key] = array(field, item => typeof item === 'number' ? number(item, true) : string(item, 64))
    else if (key === 'group_id' && typeof field === 'number' && Number.isSafeInteger(field) && field >= -1) result[key] = field
    else result[key] = string(field, key === 'search' ? 4096 : 65536)
  }
  for (const key of ['proxy_ids', 'folder_ids', 'tag_ids']) result[key] = ids(source[key])
  for (const key of ['include_direct', 'include_uncategorized']) result[key] = boolean(source[key])
  return clean(result)
}

function choices(value: unknown, group = false, taxonomy = false) {
  return array(value, item => {
    const source = object(item)
    return clean({ id: number(source.id, true), name: string(source.name, 256),
      ...(group ? { platform: string(source.platform, 64), wire_platform: string(source.wire_platform, 64), provider_profile: string(source.provider_profile, 64) } : {}),
      ...(taxonomy ? { sort_order: number(source.sort_order), account_count: number(source.account_count), created_at: string(source.created_at, 64), updated_at: string(source.updated_at, 64) } : {}) })
  }, 10000)
}

function target(value: unknown) {
  const source = object(value)
  if (source.mode === 'selected') return clean({ mode: 'selected', accountIds: ids(source.accountIds), count: number(source.count) })
  if (source.mode === 'filtered') return clean({ mode: 'filtered', filters: filters(source.filters), count: number(source.count) })
  throw new Error('Invalid plugin selection target')
}

function viewProps(value: unknown) {
  const source = object(value), result: ObjectValue = {}
  for (const key of ['accountId', 'folderId']) result[key] = number(source[key], true)
  for (const key of ['accountIds', 'selectedIds', 'tagIds']) result[key] = ids(source[key])
  result.activeFolder = string(source.activeFolder, 64)
  for (const key of ['total', 'uncategorizedCount']) result[key] = number(source[key])
  for (const key of ['loading', 'error', 'initiallyExpanded']) result[key] = boolean(source[key])
  for (const key of ['folders', 'tags']) result[key] = choices(source[key], false, true)
  result.groups = choices(source.groups, true)
  result.proxies = choices(source.proxies)
  if (source.target !== undefined && source.target !== null) result.target = target(source.target)
  if (source.filters !== undefined) result.filters = filters(source.filters)
  return clean(result)
}

export function publicPluginContext(context: ObjectValue): ObjectValue {
  const result: ObjectValue = {}
  for (const key of ['contribution_id', 'operation', 'mode']) result[key] = string(context[key], 128)
  for (const key of ['account_id', 'error_id']) result[key] = number(context[key], true)
  result.account_ids = ids(context.account_ids)
  if (context.view_props !== undefined) result.view_props = viewProps(context.view_props)
  if (context.account_view_state !== undefined) result.account_view_state = viewState(context.account_view_state)
  return clean(result)
}
