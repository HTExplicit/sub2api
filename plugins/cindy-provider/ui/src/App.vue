<template>
  <CindyBalanceProbePanel v-if="mode === 'cindy-balance-probe'" :selected-ids="input.selectedIds || []" :filters="input.filters || {}" :initially-expanded="input.initiallyExpanded" />
  <CindyDuplicateInventoryPanel v-else-if="mode === 'cindy-duplicate-inventory'" />
  <CindyGroupAuditDialog v-else-if="mode === 'cindy-group-audit'" show @close="event('close')" @split="(result: CindyGroupSplitResult) => event('split', result)" />
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { emitHostEvent, usePluginContext } from '@sub2api/plugin-ui'
import CindyBalanceProbePanel from './CindyBalanceProbePanel.vue'
import CindyDuplicateInventoryPanel from './CindyDuplicateInventoryPanel.vue'
import CindyGroupAuditDialog from './CindyGroupAuditDialog.vue'
import type { CindyBalanceProbeFilters, CindyGroupSplitResult } from './api'
const context = usePluginContext()
const mode = computed(() => context.value.contribution_id)
const input = computed(() => (context.value.view_props || {}) as { selectedIds?: number[]; filters?: CindyBalanceProbeFilters; initiallyExpanded?: boolean })
function event(name: string, payload?: unknown) { void emitHostEvent(name, payload) }
</script>
