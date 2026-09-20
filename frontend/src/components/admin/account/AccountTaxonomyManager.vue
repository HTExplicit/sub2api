<template>
  <ExtensionModal :show="show" name="account-taxonomy-manager" :context="{ view_props: { folders, tags } }"
    @close="emit('close')" @event="name => { if (name === 'changed') emit('changed') }" />
</template>
<script setup lang="ts">
import ExtensionModal from '@/components/plugins/ExtensionModal.vue'
import type { AccountManagementFolder, AccountManagementTag } from '@/types'
defineProps<{ show: boolean; folders: AccountManagementFolder[]; tags: AccountManagementTag[] }>()
const emit = defineEmits<{ close: []; changed: [] }>()
</script>
