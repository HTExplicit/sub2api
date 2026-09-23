<template>
  <ExtensionWidget name="prompt-account-binding" plugin-key="codexrip.prompt-skills"
    :context="{ account_ids: accountIds, view_props: { accountIds } }"
    @event="name => { if (name === 'changed') emit('changed') }" />
</template>
<script setup lang="ts">
import ExtensionWidget from '@/components/plugins/ExtensionWidget.vue'
defineProps<{ accountIds: number[] }>()
const emit = defineEmits<{ changed: [] }>()
</script>
