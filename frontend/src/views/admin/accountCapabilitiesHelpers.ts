import type {
  CapabilityCandidate,
  CapabilityItem,
  CapabilityProbeTarget,
  CapabilityRunStatus,
} from '@/api/admin/accountCapabilities'

export function isCapabilityRunActive(status: CapabilityRunStatus): boolean {
  return ['pending', 'running', 'pausing', 'canceling'].includes(status)
}

export function isCurrentCapability(item: CapabilityItem): boolean {
  return !item.stale_config && item.is_current_scope !== false && item.status !== 'stale'
}

export function canPublishCapability(item: CapabilityItem): boolean {
  return item.status === 'succeeded' && item.result?.status === 'alive' && !!item.upstream_model && isCurrentCapability(item)
}

export function isPublishableCandidate(item: CapabilityCandidate): boolean {
  if (!item.latest_probe_item_id) return false
  // An explicit server refusal must not fall back to a successful old probe.
  return item.publishable === undefined
    ? item.probe_status === 'alive' && !item.stale
    : item.publishable === true
}

export function selectCapabilityPublicationTargets(items: CapabilityCandidate[]): { selected: CapabilityCandidate[]; omittedCount: number } {
  const eligible = [...new Map(items.filter(isPublishableCandidate).map((item) => [item.candidate_id, item])).values()]
  const buckets = new Map<string, Map<string, CapabilityCandidate[]>>()
  for (const item of eligible) {
    const key = JSON.stringify([item.account_id, item.group_name, item.public_model])
    const targets = buckets.get(key) ?? new Map<string, CapabilityCandidate[]>()
    const evidence = targets.get(item.upstream_model) ?? []
    evidence.push(item)
    targets.set(item.upstream_model, evidence)
    buckets.set(key, targets)
  }
  const tierRank = { standard: 0, vip: 1, ssvip: 2 }
  const lexical = (left: string, right: string): number => left < right ? -1 : left > right ? 1 : 0
  const selected: CapabilityCandidate[] = []
  for (const targets of buckets.values()) {
    const ranked = [...targets.entries()].map(([upstream, evidence]) => ({
      upstream,
      evidence,
      protocols: new Set(evidence.map((item) => item.protocol)).size,
      tier: Math.max(...evidence.map((item) => tierRank[item.tier])),
      exact: upstream === evidence[0]!.public_model,
    }))
    ranked.sort((left, right) => right.protocols - left.protocols || right.tier - left.tier ||
      Number(right.exact) - Number(left.exact) || lexical(left.upstream, right.upstream))
    // Keep every selected protocol observation for the chosen exact target.
    // Other targets stay in the inventory; only this publication draft narrows.
    selected.push(...ranked[0]!.evidence)
  }
  return { selected, omittedCount: eligible.length - selected.length }
}

export function deduplicateProbeTargets(items: CapabilityProbeTarget[]): CapabilityProbeTarget[] {
  const result = new Map<string, CapabilityProbeTarget>()
  for (const item of items) {
    // Preserve exact upstream IDs, including namespace, case and suffix.
    const key = JSON.stringify([item.account_id, item.upstream_model, item.protocol, item.profile])
    const previous = result.get(key)
    result.set(key, { ...item, aliases: [...new Set([...(previous?.aliases ?? []), ...item.aliases])] })
  }
  return [...result.values()]
}

export function parseCapabilityIDs(value: unknown): number[] {
  const values = Array.isArray(value) ? value : [value]
  return [...new Set(values.flatMap((item) => String(item ?? '').split(','))
    .filter((item) => /^\d+$/.test(item))
    .map(Number).filter((item) => Number.isSafeInteger(item) && item > 0))]
}

export function makeCapabilityIdempotencyKey(): string {
  return `account_capability_${globalThis.crypto?.randomUUID?.() ?? `${Date.now()}_${Math.random().toString(16).slice(2)}`}`
}

export function capabilityCodeLabel(
  code: string,
  section: 'reasonLabels' | 'states',
  translate: (key: string) => string,
  hasKey: (key: string) => boolean,
): string {
  // Only exact structured codes enter the local message catalog. Do not
  // reinterpret provider prose, change its case or guess a translated error.
  if (!/^[a-z][a-z0-9_]*$/.test(code)) return code
  const key = `admin.accountCapabilities.${section}.${code}`
  return hasKey(key) ? translate(key) : code
}
