import type { Account } from '@/types'

export function cindyAccount(id = 1): Account {
  return {
    id, name: `Cindy ${id}`, platform: 'cindy', type: 'apikey', status: 'active', schedulable: false,
    concurrency: 1, priority: 0, rate_multiplier: 1, credentials: {}, extra: { privacy_mode: 'private' }, groups: [], tags: [],
    cindy_balance_insufficient: true, cindy_balance_probe_job_id: 321, cindy_balance_probe_outcome: 'healthy',
    cindy_balance_probe_checked_at: '2032-08-16T00:02:00Z'
  } as Account
}
