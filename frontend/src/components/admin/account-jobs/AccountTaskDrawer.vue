<template>
  <BaseDialog :show="store.drawerOpen" :title="store.currentJob ? t('admin.accountTasks.kinds.' + store.currentJob.kind) : t('admin.accountTasks.details')" width="wide" @close="closeResult">
    <AccountOperationProgress @close="closeResult" />
  </BaseDialog>
  <BaseDialog :show="store.historyOpen" :title="t('admin.accountTasks.title')" width="wide" @close="store.historyOpen = false">
    <div class="mb-5 flex flex-wrap items-center justify-between gap-3">
      <p class="text-sm text-muted">{{ t('admin.accountTasks.description') }}</p>
      <select v-model="historyStatus" class="input !w-auto" :aria-label="t('admin.accountTasks.allStatuses')" @change="loadHistory(1)">
        <option value="">{{ t('admin.accountTasks.allStatuses') }}</option>
        <option v-for="status in statuses" :key="status" :value="status">{{ t('admin.accountTasks.statuses.' + status) }}</option>
      </select>
    </div>
    <div class="divide-y divide-line" :aria-busy="store.loadingJobs">
      <button v-for="operation in store.recentJobs" :key="operation.id" type="button" class="flex w-full items-center justify-between gap-4 py-4 text-left transition-colors hover:bg-raised" @click="store.openJob(operation.id)">
        <span class="min-w-0">
          <span class="block text-sm font-medium text-ink">{{ t('admin.accountTasks.kinds.' + operation.kind) }}</span>
          <span class="mt-1 block text-xs text-muted">{{ formatTime(operation.created_at) }} · {{ t('admin.accountTasks.resultSummary', { succeeded: operation.succeeded_count, failed: operation.failed_count, canceled: operation.canceled_count }) }}</span>
        </span>
        <span class="shrink-0 text-xs" :class="operation.failed_count ? 'text-amber-600 dark:text-amber-400' : 'text-primary-600 dark:text-primary-400'">{{ t('admin.accountTasks.statuses.' + operation.status) }}</span>
      </button>
      <p v-if="!store.recentJobs.length" class="py-10 text-center text-sm text-muted">{{ t(store.loadingJobs ? 'common.loading' : 'admin.accountTasks.noTasks') }}</p>
    </div>
    <template #footer>
      <span class="mr-auto text-xs text-muted">{{ store.jobPage.page }} / {{ Math.max(1, Math.ceil(store.jobPage.total / store.jobPage.pageSize)) }}</span>
      <button class="btn btn-secondary btn-sm" :disabled="store.loadingJobs || store.jobPage.page <= 1" @click="loadHistory(store.jobPage.page - 1)">{{ t('admin.accountTasks.previousPage') }}</button>
      <button class="btn btn-secondary btn-sm" :disabled="store.loadingJobs || store.jobPage.page * store.jobPage.pageSize >= store.jobPage.total" @click="loadHistory(store.jobPage.page + 1)">{{ t('admin.accountTasks.nextPage') }}</button>
    </template>
  </BaseDialog>
  <Teleport to="body">
    <Transition name="operation-dock">
      <aside v-if="dockJob && !store.drawerOpen && !store.embeddedOpen" class="fixed bottom-4 right-4 z-40 w-[calc(100%-2rem)] border border-line bg-canvas shadow-outline sm:w-80" :aria-label="t('admin.accountTasks.inProgress')" data-test="operation-dock">
        <div class="flex items-center gap-3 px-4 py-3">
          <Icon :name="isTerminalAccountJob(dockJob) ? 'checkCircle' : 'refresh'" size="sm" class="shrink-0 text-primary-600" :class="!isTerminalAccountJob(dockJob) && 'animate-spin'" />
          <button class="min-w-0 flex-1 text-left" @click="store.openJob(dockJob.id)">
            <span class="block truncate text-sm font-medium text-ink">{{ t('admin.accountTasks.kinds.' + dockJob.kind) }}</span>
            <span class="mt-1 block text-xs text-muted" aria-live="polite">{{ store.connectionLost ? t('admin.accountTasks.reconnecting') : t('admin.accountTasks.statuses.' + dockJob.status) }} · {{ dockJob.processed_count }}/{{ dockJob.target_count }}</span>
          </button>
          <button v-if="isTerminalAccountJob(dockJob)" class="btn-ghost btn-icon" :aria-label="t('common.close')" @click="store.dismissJob(dockJob.id)"><Icon name="x" size="sm" /></button>
          <button v-else class="text-xs text-primary-600" @click="store.openJob(dockJob.id)">{{ t('admin.accountTasks.expand') }}</button>
        </div>
        <div v-if="store.visibleJobs.length > 1" class="flex items-center justify-between border-t border-line px-4 py-2 text-xs text-muted">
          <button :aria-label="t('admin.accountTasks.previousOperation')" @click="switchDock(-1)">←</button>
          <span>{{ dockIndex + 1 }} / {{ store.visibleJobs.length }}</span>
          <button :aria-label="t('admin.accountTasks.nextOperation')" @click="switchDock(1)">→</button>
        </div>
        <div class="h-0.5 bg-surface"><div class="h-full bg-primary-500 transition-[width]" :style="{ width: (dockJob.target_count ? Math.min(100, dockJob.processed_count / dockJob.target_count * 100) : 0) + '%' }" /></div>
      </aside>
    </Transition>
  </Teleport>
</template>
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import AccountOperationProgress from './AccountOperationProgress.vue'
import { isTerminalAccountJob, useAccountJobsStore } from '@/stores/accountJobs'
import { useAppStore } from '@/stores/app'
const { t } = useI18n()
const store = useAccountJobsStore()
const dockIndex = ref(0), historyStatus = ref('')
const statuses = ['pending', 'running', 'succeeded', 'partially_succeeded', 'failed', 'canceled']
const dockJob = computed(() => store.visibleJobs[dockIndex.value] ?? store.visibleJobs[0])
watch(() => store.visibleJobs.length, count => { dockIndex.value = Math.min(dockIndex.value, Math.max(0, count - 1)) })
watch(() => store.historyOpen, open => { if (open) historyStatus.value = '' })
function switchDock(offset: number) { dockIndex.value = (dockIndex.value + offset + store.visibleJobs.length) % store.visibleJobs.length }
function closeResult() {
  if (store.currentJob && isTerminalAccountJob(store.currentJob)) store.dismissJob(store.currentJob.id)
  else store.closeDrawer()
}
function formatTime(value: string) { const d = new Date(value); return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString() }
async function loadHistory(page: number) {
  try { await store.loadRecent({ page, status: historyStatus.value }) }
  catch { useAppStore().showError(t('admin.accountTasks.loadFailed')) }
}
</script>
<style scoped>
.operation-dock-enter-active, .operation-dock-leave-active { transition: opacity .18s ease, transform .18s ease; }
.operation-dock-enter-from, .operation-dock-leave-to { opacity: 0; transform: translateY(8px); }
</style>
