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
  if (!item.last_success_item_id && !item.latest_probe_item_id) return false
  // An explicit server refusal must not fall back to a successful old probe.
  return item.publishable === undefined
    ? item.probe_status === 'alive' && !item.stale
    : item.publishable === true
}

export function capabilityPublicationTier(publicModel: string, upstreamTier: CapabilityCandidate['tier']): 'standard' | 'vip' {
  // Only GPT has separate public standard/VIP groups. Other brands share one
  // public group while their exact upstream targets and displayed tiers remain intact.
  return publicModel.startsWith('gpt-') && upstreamTier !== 'standard' ? 'vip' : 'standard'
}

export function selectCapabilityPublicationTargets(items: CapabilityCandidate[]): { selected: CapabilityCandidate[]; omittedCount: number } {
  // The server owns eligibility and multi-branch routing. A browser-side
  // preference must not discard a successful alternative target or platform.
  const selected = [...new Map(items.filter(isPublishableCandidate).map((item) => [item.candidate_id, item])).values()]
  return { selected, omittedCount: 0 }
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
