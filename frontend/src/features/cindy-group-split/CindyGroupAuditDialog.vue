<template>
  <ExtensionModal :show="show" name="cindy-group-audit" @close="emit('close')"
    @event="(name, payload) => { if (name === 'split') emit('split', payload as CindyGroupSplitResult) }" />
</template>
<script setup lang="ts">
import ExtensionModal from '@/components/plugins/ExtensionModal.vue'
import type { CindyGroupSplitResult } from '@/api/admin/groups'
defineProps<{ show: boolean }>()
const emit = defineEmits<{ close: []; split: [result: CindyGroupSplitResult] }>()
</script>
