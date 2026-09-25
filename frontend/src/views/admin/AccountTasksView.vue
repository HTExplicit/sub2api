<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex flex-wrap items-end justify-between gap-3">
          <div class="flex flex-wrap items-end gap-3">
            <label class="block min-w-44">
              <span class="input-label">{{ t('admin.accountTasks.columns.task') }}</span>
              <select v-model="kind" class="input" @change="loadPage(1)">
                <option value="">{{ t('admin.accountTasks.allKinds') }}</option>
                <option v-for="value in jobKinds" :key="value" :value="value">{{ kindLabel(value) }}</option>
              </select>
            </label>
            <label class="block min-w-40">
              <span class="input-label">{{ t('admin.accountTasks.columns.status') }}</span>
              <select v-model="status" class="input" @change="loadPage(1)">
                <option value="">{{ t('admin.accountTasks.allStatuses') }}</option>
                <option v-for="value in jobStatuses" :key="value" :value="value">{{ statusLabel(value) }}</option>
              </select>
            </label>
          </div>
          <button type="button" class="btn btn-secondary" :disabled="store.loadingJobs" @click="loadPage(store.jobPage.page)">
            <Icon name="refresh" size="sm" />
            {{ t('common.refresh') }}
          </button>
        </div>
      </template>

      <template #table>
        <DataTable :columns="columns" :data="store.recentJobs" :loading="store.loadingJobs" row-key="id">
          <template #cell-task="{ row }">
            <div class="min-w-0">
              <div class="truncate text-sm font-medium text-gray-900 dark:text-white">{{ kindLabel(row.kind) }}</div>
              <div class="mt-0.5 text-xs text-gray-500 dark:text-dark-300">#{{ row.id }}</div>
            </div>
          </template>
          <template #cell-status="{ row }">
            <span :class="statusClass(row.status)">{{ statusLabel(row.status) }}</span>
          </template>
          <template #cell-progress="{ row }">
            <span class="tabular-nums text-gray-700 dark:text-dark-200">{{ row.processed_count }}/{{ row.target_count }}</span>
          </template>
          <template #cell-result="{ row }">
            <span class="tabular-nums text-gray-600 dark:text-dark-300">
              {{ t('admin.accountTasks.resultSummary', { succeeded: row.succeeded_count, failed: row.failed_count, canceled: row.canceled_count }) }}
            </span>
          </template>
          <template #cell-created_at="{ row }">
            <span class="whitespace-nowrap text-gray-500 dark:text-dark-300">{{ formatTime(row.created_at) }}</span>
          </template>
          <template #cell-actions="{ row }">
            <button
              type="button"
              :data-test="`task-open-${row.id}`"
              class="font-medium text-primary-600 hover:text-primary-700 dark:text-primary-300"
              @click="store.openJob(row.id)"
            >
              {{ t('admin.accountTasks.details') }}
            </button>
          </template>
        </DataTable>
      </template>

      <template #pagination>
        <Pagination
          v-if="store.jobPage.total > 0"
          :total="store.jobPage.total"
          :page="store.jobPage.page"
          :page-size="store.jobPage.pageSize"
          @update:page="loadPage"
          @update:pageSize="changePageSize"
        />
      </template>
    </TablePageLayout>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAccountJobsStore } from '@/stores/accountJobs'
import { useAppStore } from '@/stores/app'
import type { AccountJobStatus } from '@/api/admin/accountJobs'

const { t } = useI18n()
const store = useAccountJobsStore()
const appStore = useAppStore()
const kind = ref('')
const status = ref('')

const jobKinds = [
  'account_import',
  'account_import_codex',
  'account_batch_create',
  'account_bulk_update',
  'account_bulk_taxonomy',
  'account_batch_delete',
  'account_batch_clear_error',
  'account_batch_refresh',
  'account_batch_refresh_tier',
  'account_batch_update_credentials',
  'account_duplicate_review',
  'account_duplicate_merge',
  'cindy_confirmed_cleanup',
  'cindy_banned_cleanup',
]
const jobStatuses: AccountJobStatus[] = [
  'pending',
  'running',
  'succeeded',
  'partially_succeeded',
  'failed',
  'canceled',
]
const columns = [
  { key: 'task', label: t('admin.accountTasks.columns.task') },
  { key: 'status', label: t('admin.accountTasks.columns.status') },
  { key: 'progress', label: t('admin.accountTasks.columns.progress') },
  { key: 'result', label: t('admin.accountTasks.columns.result') },
  { key: 'created_at', label: t('admin.accountTasks.columns.createdAt') },
  { key: 'actions', label: t('admin.accountTasks.columns.actions') },
]

function kindLabel(value: string): string {
  return String(t(`admin.accountTasks.kinds.${value}`))
}

function statusLabel(value: AccountJobStatus): string {
  return String(t(`admin.accountTasks.statuses.${value}`))
}

function statusClass(value: AccountJobStatus): string {
  if (value === 'succeeded') return 'text-emerald-600 dark:text-emerald-300'
  if (value === 'failed') return 'text-red-600 dark:text-red-300'
  if (value === 'partially_succeeded' || value === 'canceled') return 'text-amber-600 dark:text-amber-300'
  return 'text-primary-600 dark:text-primary-300'
}

function formatTime(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '-' : date.toLocaleString()
}

async function loadPage(page: number): Promise<void> {
  try {
    await store.loadRecent({
      page,
      page_size: store.jobPage.pageSize,
      kind: kind.value,
      status: status.value,
    })
  } catch {
    appStore.showError(t('admin.accountTasks.loadFailed'))
  }
}

function changePageSize(pageSize: number): void {
  store.jobPage.pageSize = pageSize
  void loadPage(1)
}

onMounted(() => {
  void loadPage(1)
})
</script>
