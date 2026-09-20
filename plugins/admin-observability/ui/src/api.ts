import { resource } from '@sub2api/plugin-ui'
import type { PromptAuditConfig, PromptAuditEvent, PromptAuditGroup, PromptAuditRuntime, PromptAuditUpdateRequest, PromptDeletePreview, PromptDeleteResult, PromptEventFilters, PromptEventPage, PromptProbeResult, PromptAuditEndpointDraft } from './types'
import { eventFilterPayload, eventQueryParams } from './viewModel'

export const getConfig = () => resource<PromptAuditConfig>('prompt-audit.config')
export const updateConfig = (body: PromptAuditUpdateRequest) => resource<PromptAuditConfig>('prompt-audit.update', { body })
export const getRuntime = () => resource<PromptAuditRuntime>('prompt-audit.runtime')
export const listEvents = (filters: PromptEventFilters, page: number, pageSize: number) => resource<PromptEventPage>('prompt-audit.events', { query: { page, page_size: pageSize, ...eventQueryParams(filters) } })
export const getEvent = (id: number) => resource<PromptAuditEvent>('prompt-audit.event', { params: { id } })
export const deleteEvent = (id: number) => resource<PromptDeleteResult>('prompt-audit.delete', { params: { id } })
export const batchDeleteEvents = (ids: number[]) => resource<PromptDeleteResult>('prompt-audit.batch-delete', { body: { ids } })
export const previewDelete = (filters: PromptEventFilters) => resource<PromptDeletePreview>('prompt-audit.delete-preview', { body: eventFilterPayload(filters) })
export const listGroups = () => resource<PromptAuditGroup[]>('prompt-audit.groups')
export const deleteEventsByFilter = (filters: PromptEventFilters, preview: PromptDeletePreview) => resource<PromptDeleteResult>('prompt-audit.delete-by-filter', { body: {
  filter: eventFilterPayload(filters), snapshot_max_id: preview.snapshot_max_id,
  filter_hash: preview.filter_hash, confirmation_token: preview.confirmation_token, confirm: true
} })
export const probeEndpoint = (endpoint: PromptAuditEndpointDraft) => resource<PromptProbeResult>('prompt-audit.probe', { body: { endpoint: {
  id: endpoint.id, name: endpoint.name, protocol: 'openai_compatible', base_url: endpoint.base_url,
  model: endpoint.model, token: endpoint.token || undefined, timeout_ms: endpoint.timeout_ms,
  input_limit: endpoint.input_limit, enabled: endpoint.enabled
} } })
export default { getConfig, updateConfig, probeEndpoint, getRuntime, listEvents, getEvent, deleteEvent, batchDeleteEvents, previewDelete, deleteEventsByFilter, listGroups }
