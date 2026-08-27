<template>
  <section class="min-w-0 rounded-lg border border-gray-200 bg-white p-3 dark:border-dark-700 dark:bg-dark-800" data-testid="cindy-duplicate-inventory">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div class="min-w-0">
        <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.accounts.cindy.duplicateTitle') }}</h3>
        <p class="mt-1 text-xs text-gray-500 dark:text-dark-300">{{ t('admin.accounts.cindy.duplicateHint') }}</p>
      </div>
      <button type="button" class="btn btn-secondary h-8 px-3 text-xs" :disabled="loading" data-testid="cindy-duplicate-refresh" @click="load">
        {{ loading ? t('common.loading') : t('common.refresh') }}
      </button>
    </div>

    <p v-if="!loading && inventory.length === 0" class="mt-3 text-xs text-emerald-600 dark:text-emerald-300" data-testid="cindy-duplicate-empty">
      {{ t('admin.accounts.cindy.duplicateEmpty') }}
    </p>
    <div v-else-if="inventory.length > 0" class="mt-3 space-y-2">
      <article
        v-for="cluster in inventory"
        :key="cluster.identity_hash"
        class="min-w-0 rounded-md border border-gray-100 bg-gray-50 px-3 py-2 text-xs dark:border-dark-700 dark:bg-dark-900"
      >
        <div class="flex flex-wrap items-center gap-x-4 gap-y-1">
          <span class="font-mono text-gray-500 dark:text-dark-300">{{ shortHash(cluster.identity_hash) }}</span>
          <span class="text-gray-700 dark:text-gray-200">
            {{ t('admin.accounts.cindy.duplicateOwner', { id: cluster.proposed_owner_id || '-' }) }}
          </span>
        </div>
        <p class="mt-1 break-words text-gray-500 dark:text-dark-300">
          {{ t('admin.accounts.cindy.duplicateOthers', { ids: cluster.other_account_ids.join(', ') }) }}
        </p>
      </article>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import type { CindyDuplicateIdentityGroup } from '@/api/admin/accounts'

const { t } = useI18n()
const appStore = useAppStore()
const inventory = ref<CindyDuplicateIdentityGroup[]>([])
const loading = ref(false)

function shortHash(value: string): string {
  if (value.length <= 24) return value
  return `${value.slice(0, 12)}…${value.slice(-8)}`
}

async function load(): Promise<void> {
  if (loading.value) return
  loading.value = true
  try {
    inventory.value = await adminAPI.accounts.getCindyDuplicateIdentityInventory()
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.accounts.cindy.duplicateLoadFailed'))
  } finally {
    loading.value = false
  }
}

onMounted(() => { void load() })
</script>
