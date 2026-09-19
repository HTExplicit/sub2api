import type { AccountQuotaWindow, AccountUsageInfo } from '@/types'

// Compatibility for an already-open UI receiving a pre-upgrade response. New
// servers supply the actual periods; never recompute monetary estimates here.
export function accountQuotaWindows(usage?: AccountUsageInfo | null): AccountQuotaWindow[] {
  if (!usage) return []
  if (usage.quota_windows) return usage.quota_windows
  const out: AccountQuotaWindow[] = []
  for (const [id, minutes, progress] of [
    ['5h', 300, usage.five_hour],
    ['7d', 10080, usage.seven_day]
  ] as const) {
    if (!progress) continue
    out.push({
      id, window_minutes: minutes,
      utilization: Number.isFinite(progress.utilization) ? progress.utilization : 0,
      resets_at: progress.resets_at || null,
      remaining_seconds: progress.remaining_seconds,
      expired: false, window_stats: progress.window_stats || undefined,
      estimate: { status: 'insufficient_data' }
    })
  }
  return out
}
