<template>
  <AccountsView scope="cindy">
    <template #scope-tools="{ selectedIds, filters }">
      <CindyBalanceProbePanel :selected-ids="selectedIds" :filters="filters" />
    </template>
  </AccountsView>
</template>

<script setup lang="ts">
import AccountsView from './AccountsView.vue'
import CindyBalanceProbePanel from '@/features/cindy-balance-probe/CindyBalanceProbePanel.vue'
</script>
