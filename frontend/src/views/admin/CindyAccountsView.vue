<template>
  <AccountsView scope="cindy">
    <template #scope-tools="{ selectedIds, filters }">
      <div class="min-w-0 space-y-3">
        <CindyDuplicateInventoryPanel />
        <CindyBalanceProbePanel :selected-ids="selectedIds" :filters="filters" />
      </div>
    </template>
  </AccountsView>
</template>

<script setup lang="ts">
import AccountsView from './AccountsView.vue'
import CindyBalanceProbePanel from '@/features/cindy-balance-probe/CindyBalanceProbePanel.vue'
import CindyDuplicateInventoryPanel from '@/features/cindy-duplicate-inventory/CindyDuplicateInventoryPanel.vue'
</script>
