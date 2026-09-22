<template>
  <ExtensionDisplay name="cindy-probe-summary" :show-label="showLabel" :values="{ job: account.cindy_balance_probe_job_id, outcome: account.cindy_balance_probe_outcome, checked_at: account.cindy_balance_probe_checked_at }" />
</template>
<script setup lang="ts">
import ExtensionDisplay from '@/components/plugins/ExtensionDisplay.vue'
import type { Account } from '@/types'
defineProps<{ account: Account; showLabel?: boolean }>()
</script>
