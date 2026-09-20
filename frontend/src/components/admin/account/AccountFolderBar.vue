<template>
  <ExtensionWidget name="account-taxonomy-navigation" :context="{ view_props: props }" @event="receive" />
</template>
<script setup lang="ts">
import ExtensionWidget from '@/components/plugins/ExtensionWidget.vue'
import type { AccountManagementFolder } from '@/types'
const props = defineProps<{ folders: AccountManagementFolder[]; activeFolder: string; total?: number; uncategorizedCount?: number; loading?: boolean; error?: boolean }>()
const emit = defineEmits<{ select: [value: string]; manage: []; retry: [] }>()
function receive(name: string, payload: unknown) {
  if (name === 'select' && typeof payload === 'string') emit('select', payload)
  else if (name === 'manage') emit('manage')
  else if (name === 'retry') emit('retry')
}
</script>
