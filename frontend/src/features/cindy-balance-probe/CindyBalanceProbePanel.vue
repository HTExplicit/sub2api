<template>
  <ExtensionWidget name="cindy-balance-probe" :context="{ view_props: { selectedIds, filters, initiallyExpanded } }" />
</template>
<script setup lang="ts">
import ExtensionWidget from '@/components/plugins/ExtensionWidget.vue'
import type { CindyBalanceProbeFilters } from '@/api/admin/cindyBalanceProbe'
defineProps<{ selectedIds: number[]; filters: CindyBalanceProbeFilters; initiallyExpanded?: boolean }>()
</script>
