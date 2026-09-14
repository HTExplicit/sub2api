<template>
  <div data-test="account-card-grid" class="min-h-0 overflow-y-auto p-0.5">
    <div v-if="loading" class="grid grid-cols-1 gap-3 lg:grid-cols-2 2xl:grid-cols-3">
      <div v-for="index in 6" :key="index" class="h-52 animate-pulse rounded-md border border-gray-200 bg-gray-50 dark:border-dark-700 dark:bg-dark-800" />
    </div>
    <div v-else-if="accounts.length === 0" class="flex min-h-52 flex-col items-center justify-center text-gray-400">
      <Icon name="inbox" size="xl" />
      <span class="mt-2 text-sm">{{ t('empty.noData') }}</span>
    </div>
    <div v-else class="grid grid-cols-1 gap-3 lg:grid-cols-2 2xl:grid-cols-3">
      <article
        v-for="account in accounts"
        :key="account.id"
        class="cursor-pointer rounded-md border bg-white p-4 transition-colors hover:border-gray-300 hover:bg-gray-50/60 dark:bg-dark-900 dark:hover:border-dark-500 dark:hover:bg-dark-800/70"
        :class="selectedSet.has(account.id) ? 'border-primary-300 ring-1 ring-primary-200 dark:border-primary-700 dark:ring-primary-900' : 'border-gray-200 dark:border-dark-700'"
        @click="emit('rowClick', account)"
      >
        <div class="flex items-start gap-3">
          <AccountSelectionCheckbox :checked="selectedSet.has(account.id)"
          :label="t('admin.accounts.selectAccount', { name: account.name })"
          @change="emit('toggle', account.id)" />
          <div class="min-w-0 flex-1">
            <div class="flex min-w-0 items-center gap-2">
              <button type="button" class="truncate text-left text-sm font-semibold text-gray-900 dark:text-white" @click.stop="emit('rowClick', account)">{{ account.name }}</button>
              <span class="shrink-0 font-mono text-[10px] text-gray-400">#{{ account.id }}</span>
            </div>
            <p class="mt-0.5 truncate text-xs text-gray-500 dark:text-dark-300">{{ displayEmail(account) || '-' }}</p>
          </div>
          <div class="flex items-center gap-0.5" @click.stop>
            <button type="button" class="icon-button" :title="t('common.edit')" @click="emit('edit', account)">
              <Icon name="edit" size="sm" />
            </button>
            <button type="button" class="icon-button" :title="t('common.more')" @click="emit('more', account, $event)">
              <Icon name="more" size="sm" />
            </button>
          </div>
        </div>

        <div class="mt-3 flex flex-wrap items-center gap-1.5">
          <AccountIdentityBadges :account="account" />
          <AccountStatusIndicator :account="account" @show-temp-unsched="emit('showTempUnsched', account)" />
        </div>

        <div v-if="showCindyProbe" class="mt-3 min-w-0 border-t border-gray-100 pt-3 dark:border-dark-700">
          <CindyBalanceProbeSummary :account="account" show-label />
        </div>

        <div class="mt-3 border-t border-gray-100 pt-3 dark:border-dark-700" data-test="account-card-usage">
          <AccountUsageCell
            :account="account"
            :today-stats="todayStats[String(account.id)] ?? null"
            :today-stats-loading="todayStatsLoading"
            :today-stats-error="todayStatsError"
            :today-stats-updated-at="todayStatsUpdatedAt"
            :manual-refresh-token="manualRefreshToken"
            :status-now="statusNow"
            variant="list"
            read-only
          />
          <AccountCapacityCell :account="account" compact class="mt-2" />
        </div>

        <div class="mt-3 grid grid-cols-2 gap-x-4 gap-y-3 border-t border-gray-100 pt-3 dark:border-dark-700" data-test="account-card-taxonomy">
          <div class="min-w-0">
            <div class="text-[10px] font-medium uppercase text-gray-400">{{ t('admin.accounts.classification') }}</div>
            <div class="mt-1 truncate text-xs font-medium text-gray-700 dark:text-gray-200">
              {{ account.management_folder?.name || t('admin.accounts.folderUncategorized') }}
            </div>
            <div class="mt-1 flex min-h-5 flex-wrap gap-1">
              <span v-for="tag in account.tags || []" :key="tag.id" class="rounded bg-gray-100 px-1.5 py-0.5 text-[10px] text-gray-600 dark:bg-dark-700 dark:text-gray-300">
                {{ tag.name }}
              </span>
              <span v-if="!(account.tags || []).length" class="text-xs text-gray-400">-</span>
            </div>
          </div>
          <div class="min-w-0">
            <div class="text-[10px] font-medium uppercase text-gray-400">{{ t('admin.accounts.routing') }}</div>
            <AccountGroupsCell class="mt-1" :groups="account.groups" />
            <div class="mt-1 truncate text-xs text-gray-500 dark:text-dark-300">
              {{ account.proxy?.name || t('admin.accounts.directConnection') }}
            </div>
          </div>
        </div>

      </article>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import AccountCapacityCell from '@/components/account/AccountCapacityCell.vue'
import AccountStatusIndicator from '@/components/account/AccountStatusIndicator.vue'
import AccountUsageCell from '@/components/account/AccountUsageCell.vue'
import AccountIdentityBadges from '@/components/account/AccountIdentityBadges.vue'
import AccountSelectionCheckbox from '@/components/account/AccountSelectionCheckbox.vue'
import AccountGroupsCell from '@/components/account/AccountGroupsCell.vue'
import Icon from '@/components/icons/Icon.vue'
import CindyBalanceProbeSummary from '@/features/cindy-balance-probe/CindyBalanceProbeSummary.vue'
import type { Account, WindowStats } from '@/types'

const props = withDefaults(defineProps<{
  accounts: Account[]
  loading: boolean
  selectedIds: number[]
  todayStats: Record<string, WindowStats>
  todayStatsLoading: boolean
  todayStatsError: boolean
  todayStatsUpdatedAt: number | null
  manualRefreshToken: number
  statusNow: number
  showCindyProbe?: boolean
}>(), {
  showCindyProbe: false,
})

const emit = defineEmits<{
  rowClick: [account: Account]
  toggle: [id: number]
  edit: [account: Account]
  more: [account: Account, event: MouseEvent]
  showTempUnsched: [account: Account]
}>()

const { t } = useI18n()
const selectedSet = computed(() => new Set(props.selectedIds))

const displayEmail = (account: Account) => String(
  account.extra?.email_address || account.extra?.email || account.credentials?.email || account.parent_email || ''
)
</script>
