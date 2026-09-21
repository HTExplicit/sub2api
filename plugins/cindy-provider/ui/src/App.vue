<template>
  <CindyBalanceProbePanel v-if="mode === 'cindy-balance-probe'" :selected-ids="input.selectedIds || []" :filters="input.filters || {}" :initially-expanded="input.initiallyExpanded" />
  <CindyDuplicateInventoryPanel v-else-if="mode === 'cindy-duplicate-inventory'" />
  <CindyGroupAuditDialog v-else-if="mode === 'cindy-group-audit'" show @close="event('close')" @split="(result: CindyGroupSplitResult) => event('split', result)" />
  <CindyAccountControls v-else-if="mode === 'cindy-account-controls'" />
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { emitHostEvent, usePluginContext, type AccountViewStateV1 } from '@sub2api/plugin-ui'
import CindyBalanceProbePanel from './CindyBalanceProbePanel.vue'
import CindyDuplicateInventoryPanel from './CindyDuplicateInventoryPanel.vue'
import CindyGroupAuditDialog from './CindyGroupAuditDialog.vue'
import CindyAccountControls from './CindyAccountControls.vue'
import type { CindyBalanceProbeFilters, CindyGroupSplitResult } from './api'
const context = usePluginContext()
const mode = computed(() => context.value.contribution_id)
const input = computed(() => {
  const state = context.value.account_view_state as AccountViewStateV1 | undefined
  if (!state) return (context.value.view_props || {}) as { selectedIds?: number[]; filters?: CindyBalanceProbeFilters; initiallyExpanded?: boolean }
  const query = state.query, predicate = { ...state.base_query, ...state.preset.query }
  const filters: CindyBalanceProbeFilters = {
    platforms: query.platforms, types: query.types, statuses: query.statuses, plans: query.plans,
    proxy_ids: (query.proxies || []).filter(value => value !== 'direct').map(Number), include_direct: query.proxies?.includes('direct'),
    folder_ids: (query.folders || []).filter(value => value !== 'uncategorized').map(Number), include_uncategorized: query.folders?.includes('uncategorized'),
    tag_ids: query.tags, account_ids: query.account_ids, group_id: query.group_id, privacy_mode: query.privacy_mode,
    search: query.search, sort_by: query.sort_by, sort_order: query.sort_order,
    cindy_balance_status: predicate.cindy_balance_status, cindy_health_status: predicate.cindy_health_status
  }
  return { selectedIds: state.selected_ids, filters, initiallyExpanded: false }
})
function event(name: string, payload?: unknown) { void emitHostEvent(name, payload) }
</script>
