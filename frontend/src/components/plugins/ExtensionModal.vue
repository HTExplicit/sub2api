<template>
  <ExtensionDialog :contribution="selected" :context="context" :account-ids="accountIds" :account-id="accountId" width="wide"
    :origin-view="independentCore ? null : originView"
    @close="emit('close')" @job="job => emit('job', job)" @event="(name, payload) => name === 'close' ? emit('close') : emit('event', name, payload)" />
</template>
<script setup lang="ts">
import { computed } from 'vue'
import type { AccountJob } from '@/api/admin/accountJobs'
import ExtensionDialog from './ExtensionDialog.vue'
import type { CapturedAccountView } from '@/composables/useAccountViewContext'
import { useRetainedContribution } from './useRetainedContribution'
const props = withDefaults(defineProps<{ show: boolean; name: string; pluginKey?: string; context?: Record<string, unknown>; accountIds?: number[]; accountId?: number; originView?: CapturedAccountView; independentCore?: boolean }>(), { context: () => ({}), accountIds: () => [] })
const emit = defineEmits<{ close: []; event: [name: string, payload: unknown]; job: [job: AccountJob] }>()
const { contribution } = useRetainedContribution(() => ({ id: props.name, pluginKey: props.pluginKey }))
const selected = computed(() => props.show ? contribution.value || null : null)
</script>
