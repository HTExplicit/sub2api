<template>
  <BaseDialog :show="show" :title="operation ? t(`admin.accountTasks.kinds.${operation.kind}`) : title" :width="width" @close="close">
    <template v-if="job">
      <details v-if="showConfiguration" class="mb-5 border-b border-line pb-3">
        <summary class="cursor-pointer text-xs text-muted">{{ t('admin.accountTasks.configuration') }}</summary>
        <fieldset disabled class="mt-3 max-h-52 overflow-y-auto opacity-70"><slot /></fieldset>
      </details>
      <AccountOperationProgress @close="close" />
    </template>
    <slot v-else />
    <template v-if="!job && $slots.footer" #footer><slot name="footer" /></template>
  </BaseDialog>
</template>
<script setup lang="ts">
import { computed, defineAsyncComponent, onBeforeUnmount, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { useAccountJobsStore } from '@/stores/accountJobs'
import type { AccountJob } from '@/api/admin/accountJobs'
const props = withDefaults(defineProps<{ show: boolean; title: string; job?: AccountJob | null; showConfiguration?: boolean; width?: 'narrow' | 'normal' | 'wide' | 'extra-wide' | 'full' }>(), { job: null, width: 'wide', showConfiguration: true })
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const AccountOperationProgress = defineAsyncComponent(() => import('./AccountOperationProgress.vue'))
const operations = shallowRef<ReturnType<typeof useAccountJobsStore> | null>(null)
const operation = computed(() => props.job ? operations.value?.currentJob ?? props.job : null)
let mounted = true
watch(() => props.job, async job => {
  if (job) {
    const { useAccountJobsStore } = await import('@/stores/accountJobs')
    const store = useAccountJobsStore()
    operations.value = store
    if (props.job?.id !== job.id) return
    store.track(job, { open: mounted && props.show, embedded: true })
    if (mounted && props.show) void store.loadCurrent(job.id).catch(() => { store.connectionLost = true })
  }
}, { immediate: true })
watch(() => props.show, show => { if (!show && props.job && operations.value?.embeddedOpen) operations.value.closeDrawer() })
function close() {
  const store = operations.value
  if (props.job && store) {
    const current = store.currentJob
    if (current && ['succeeded', 'partially_succeeded', 'failed', 'canceled'].includes(current.status)) store.dismissJob(current.id)
    else store.closeDrawer()
  }
  emit('close')
}
onBeforeUnmount(() => { mounted = false; if (props.job && operations.value?.embeddedOpen) operations.value.closeDrawer() })
</script>
