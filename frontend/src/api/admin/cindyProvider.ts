import { apiClient } from '../client'

export interface CindyProviderSettings {
  balance_detection: boolean
  catalog_enabled: boolean
  search_enabled: boolean
}

export interface CindyProviderCatalogEntry {
  public_id?: string
  id?: string
  model_id?: string
  live_upstream_id?: string
  upstream_model?: string
  endpoints?: string[]
}

export async function getCindyProviderSettings(signal?: AbortSignal): Promise<CindyProviderSettings> {
  return (await apiClient.get<CindyProviderSettings>('/admin/settings/cindy-provider', { signal })).data
}

export async function updateCindyProviderSettings(settings: CindyProviderSettings): Promise<CindyProviderSettings> {
  return (await apiClient.put<CindyProviderSettings>('/admin/settings/cindy-provider', settings)).data
}

export async function getCindyProviderCatalog(signal?: AbortSignal): Promise<CindyProviderCatalogEntry[]> {
  const { data } = await apiClient.get<CindyProviderCatalogEntry[]>('/admin/settings/cindy-provider/catalog', { signal })
  if (!Array.isArray(data)) throw new Error('Invalid Cindy catalog response')
  return data
}
