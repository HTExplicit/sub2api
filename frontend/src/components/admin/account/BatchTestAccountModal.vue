<template>
  <ExtensionModal :show="show" name="account-batch-test" plugin-key="codexrip.account-tools" :account-ids="accountIds" :context="{ view_props: { accountIds } }"
    @close="emit('close')" @job="job => emit('submitted', job)" />
</template>
<script setup lang="ts">
import ExtensionModal from '@/components/plugins/ExtensionModal.vue'
import type { AccountJob } from '@/api/admin/accountJobs'
defineProps<{ show: boolean; accountIds: number[] }>()
const emit = defineEmits<{ close: []; submitted: [job: AccountJob] }>()
</script>
