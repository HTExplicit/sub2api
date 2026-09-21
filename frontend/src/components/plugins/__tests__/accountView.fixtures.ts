import manifest from '../../../../../plugins/cindy-provider/manifest.source.json'
import type { PluginContribution } from '@/api/admin/plugins'
import type { Account } from '@/types'

export const packageSHA = 'a'.repeat(64)
export const definitionSHA = 'b'.repeat(64)
export function viewContributions(available = true): PluginContribution[] {
  return manifest.contributions.map(item => ({ ...structuredClone(item), plugin_id: 7, plugin_key: manifest.id,
    package_sha256: packageSHA, view_definition_digest: item.slot === 'account.view.v1' ? definitionSHA : undefined,
    account_scope: { version: 1, bindings: [{ platform: 'cindy', account_type: 'apikey', rollout_percent: 100 }] }, available })) as unknown as PluginContribution[]
}
export const cindyView = () => viewContributions().find(item => item.slot === 'account.view.v1')!
export function cindyAccount(id = 1): Account {
  return { id, name: `Cindy ${id}`, platform: 'cindy', type: 'apikey', status: 'active', schedulable: true,
    concurrency: 1, priority: 0, rate_multiplier: 1, credentials: {}, extra: {}, groups: [], tags: [],
    cindy_balance_insufficient: true, cindy_balance_probe_job_id: 321, cindy_balance_probe_outcome: 'healthy',
    cindy_balance_probe_checked_at: '2032-08-16T00:02:00Z',
    account_view_facts: { version: 1, status: 'unschedulable', plan: 'pro', privacy_mode: 'private',
      canonical_cindy: true, cindy_balance_insufficient: true, cindy_banned: false } } as Account
}
