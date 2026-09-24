import { describe, expect, it } from 'vitest'
import { cindyLegacyQuery } from '../cindyLegacyRedirect'

describe('legacy Cindy account-view redirect', () => {
  it.each([
    ['banned', 'cindy_health_status', 'cindy_balance_status'],
    ['insufficient', 'cindy_balance_status', 'cindy_health_status']
  ])('keeps the explicit %s preset and unrelated account filters', (preset, retained, removed) => {
    const query = { view_preset: preset, cindy_only: 'false', [removed]: 'stale', account_ids: '7,11', search: 'needle', tags: ['2', '4'], proxies: '3', view_owner: 'codexrip.cindy-provider', view_id: 'cindy-accounts' }
    const mapped = cindyLegacyQuery(query)
    expect(mapped).toEqual({ cindy_only: 'true', [retained]: preset, account_ids: '7,11', search: 'needle', tags: ['2', '4'], proxies: '3' })
    expect(query.view_preset).toBe(preset)
    expect(query.cindy_only).toBe('false')
  })

  it.each([null, 'cindy', 'unknown'])('keeps the native Cindy default for %s without losing selected IDs or search', preset => {
    expect(cindyLegacyQuery({ view_preset: preset, account_ids: '7,11', search: 'needle' })).toEqual({ cindy_only: 'true', account_ids: '7,11', search: 'needle' })
  })
})
