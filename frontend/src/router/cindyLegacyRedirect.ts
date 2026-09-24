import type { LocationQuery, LocationQueryRaw } from 'vue-router'

export function cindyLegacyQuery(query: LocationQuery): LocationQueryRaw {
  const mapped: LocationQueryRaw = { ...query, cindy_only: 'true' }
  const preset = query.view_preset
  delete mapped.view_owner
  delete mapped.view_id
  delete mapped.view_preset
  if (preset === 'banned') {
    mapped.cindy_health_status = 'banned'
    delete mapped.cindy_balance_status
  } else if (preset === 'insufficient') {
    mapped.cindy_balance_status = 'insufficient'
    delete mapped.cindy_health_status
  } else if (preset === 'cindy') {
    delete mapped.cindy_health_status
    delete mapped.cindy_balance_status
  }
  return mapped
}
