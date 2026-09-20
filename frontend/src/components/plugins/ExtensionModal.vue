<template>
  <ExtensionDialog :contribution="selected" :context="context" :account-ids="accountIds" :account-id="accountId" width="wide"
    @close="emit('close')" @job="job => emit('job', job)" @event="(name, payload) => name === 'close' ? emit('close') : emit('event', name, payload)" />
</template>
<script setup lang="ts">
import { computed, shallowRef, watch } from 'vue'
import { usePluginExtensions } from '@/stores/pluginExtensions'
import type { PluginContribution } from '@/api/admin/plugins'
import type { AccountJob } from '@/api/admin/accountJobs'
import ExtensionDialog from './ExtensionDialog.vue'
const props = withDefaults(defineProps<{ show: boolean; name: string; context?: Record<string, unknown>; accountIds?: number[]; accountId?: number }>(), { context: () => ({}), accountIds: () => [] })
const emit = defineEmits<{ close: []; event: [name: string, payload: unknown]; job: [job: AccountJob] }>()
const registry = usePluginExtensions()
const last = shallowRef<PluginContribution | null>(null)
const current = computed(() => registry.items.find(item => item.id === props.name))
watch(current, value => { if (value) last.value = value }, { immediate: true })
const selected = computed(() => props.show ? current.value || last.value : null)
</script>
