<template>
  <ExtensionModal :show="show" name="account-taxonomy-bulk" plugin-key="codexrip.account-tools" :context="{ view_props: { target, folders, tags } }"
    :account-ids="target?.mode === 'selected' ? target.accountIds : []"
    @close="emit('close')" @job="job => emit('updated', job)" @event="name => { if (name === 'stale') emit('stale') }" />
</template>
<script setup lang="ts">
import ExtensionModal from '@/components/plugins/ExtensionModal.vue'
import type { AccountManagementFolder, AccountManagementTag } from '@/types'
import type { AccountListFilters } from '@/api/admin/accounts'
import type { AccountJob } from '@/api/admin/accountJobs'
export type AccountBulkTaxonomyTarget =
  | { mode: 'selected'; accountIds: number[]; count: number }
  | { mode: 'filtered'; filters: AccountListFilters; count: number }
defineProps<{ show: boolean; target: AccountBulkTaxonomyTarget | null; folders: AccountManagementFolder[]; tags: AccountManagementTag[] }>()
const emit = defineEmits<{ close: []; updated: [job: AccountJob]; stale: [] }>()
</script>
