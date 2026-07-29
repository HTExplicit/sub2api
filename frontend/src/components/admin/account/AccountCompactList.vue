<template>
  <div class="min-h-0 overflow-y-auto" data-test="account-compact-list">
    <div v-if="loading" class="space-y-px">
      <div v-for="index in 6" :key="index" class="h-16 animate-pulse border-b border-gray-100 bg-gray-50/70 dark:border-dark-700 dark:bg-dark-800/60" />
    </div>
    <div v-else-if="accounts.length === 0" class="flex min-h-52 flex-col items-center justify-center text-gray-400">
      <Icon name="inbox" size="xl" />
      <span class="mt-2 text-sm">{{ t('empty.noData') }}</span>
    </div>
    <button
      v-for="account in accounts"
      v-else
      :key="account.id"
      type="button"
      class="grid w-full grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1.5 border-b border-gray-100 px-3 py-2.5 text-left transition-colors last:border-b-0 hover:bg-gray-50 dark:border-dark-700 dark:hover:bg-dark-800 lg:grid-cols-[auto_minmax(12rem,1.2fr)_minmax(8rem,0.65fr)_minmax(16rem,1fr)_minmax(12rem,0.9fr)_auto] lg:gap-y-0"
      :class="selectedIds.includes(account.id) ? 'bg-primary-50/50 dark:bg-primary-900/10' : ''"
      @click="emit('rowClick', account)"
    >
      <input
        type="checkbox"
        class="row-span-2 h-4 w-4 self-start rounded border-gray-300 text-primary-600 focus:ring-primary-500 lg:row-span-1 lg:self-center"
        :checked="selectedIds.includes(account.id)"
        :aria-label="t('admin.accounts.selectAccount', { name: account.name })"
        @click.stop
        @change.stop="emit('toggle', account.id)"
      />

      <div class="min-w-0">
        <div class="flex min-w-0 items-center gap-2">
          <span class="truncate text-sm font-semibold text-gray-900 dark:text-white">{{ account.name }}</span>
          <span class="shrink-0 font-mono text-[10px] text-gray-400">#{{ account.id }}</span>
        </div>
        <div class="mt-0.5 truncate text-xs text-gray-500 dark:text-dark-300">{{ displayEmail(account) || '-' }}</div>
        <div class="mt-1 flex flex-wrap gap-1 lg:hidden">
          <PlatformTypeBadge
            :platform="account.platform"
            :type="account.type"
            :auth-mode="authMode(account)"
            :plan-type="planType(account)"
          />
        </div>
      </div>

      <div class="hidden min-w-0 lg:block">
        <PlatformTypeBadge
          :platform="account.platform"
          :type="account.type"
          :auth-mode="authMode(account)"
          :plan-type="planType(account)"
        />
      </div>

      <div class="col-start-2 row-start-2 min-w-0 lg:col-start-auto lg:row-start-auto" data-test="account-compact-usage">
        <AccountUsageCell
          :account="account"
          :today-stats="todayStats[String(account.id)] ?? null"
          :today-stats-loading="todayStatsLoading"
          :today-stats-error="todayStatsError"
          :today-stats-updated-at="todayStatsUpdatedAt"
          :manual-refresh-token="manualRefreshToken"
          :status-now="statusNow"
          variant="compact"
          read-only
        />
        <AccountCapacityCell :account="account" compact class="mt-1" />
      </div>

      <div class="hidden min-w-0 lg:block">
        <div class="flex items-center gap-2">
          <AccountStatusIndicator :account="account" @show-temp-unsched="emit('showTempUnsched', account)" />
          <span class="text-xs text-gray-400">{{ account.management_folder?.name || t('admin.accounts.folderUncategorized') }}</span>
        </div>
        <div class="mt-1 truncate text-xs text-gray-500 dark:text-dark-300">
          {{ routeSummary(account) }}
        </div>
      </div>

      <div class="col-start-3 row-span-2 row-start-1 flex items-center gap-0.5 self-start lg:col-start-auto lg:row-span-1 lg:row-start-auto lg:self-center" @click.stop>
        <button type="button" class="icon-button" :title="t('common.edit')" @click="emit('edit', account)">
          <Icon name="edit" size="sm" />
        </button>
        <button type="button" class="icon-button" :title="t('common.more')" @click="emit('more', account, $event)">
          <Icon name="more" size="sm" />
        </button>
      </div>
    </button>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import AccountCapacityCell from '@/components/account/AccountCapacityCell.vue'
import AccountStatusIndicator from '@/components/account/AccountStatusIndicator.vue'
import AccountUsageCell from '@/components/account/AccountUsageCell.vue'
import PlatformTypeBadge from '@/components/common/PlatformTypeBadge.vue'
import Icon from '@/components/icons/Icon.vue'
import type { Account, WindowStats } from '@/types'

defineProps<{
  accounts: Account[]
  loading: boolean
  selectedIds: number[]
  todayStats: Record<string, WindowStats>
  todayStatsLoading: boolean
  todayStatsError: boolean
  todayStatsUpdatedAt: number | null
  manualRefreshToken: number
  statusNow: number
}>()

const emit = defineEmits<{
  rowClick: [account: Account]
  toggle: [id: number]
  edit: [account: Account]
  more: [account: Account, event: MouseEvent]
  showTempUnsched: [account: Account]
}>()

const { t } = useI18n()

const displayEmail = (account: Account) => String(
  account.extra?.email_address || account.extra?.email || account.credentials?.email || account.parent_email || ''
)

const authMode = (account: Account) => typeof account.credentials?.auth_mode === 'string'
  ? account.credentials.auth_mode
  : undefined

const planType = (account: Account) => String(
  (account.extra?.grok_billing_snapshot as Record<string, unknown> | undefined)?.plan ||
  (account.extra?.grok_quota_snapshot as Record<string, unknown> | undefined)?.subscription_tier ||
  account.credentials?.subscription_tier || account.extra?.subscription_tier || account.credentials?.plan_type || ''
)

const routeSummary = (account: Account) => {
  const groups = account.groups?.map(group => group.name).filter(Boolean) || []
  const proxy = account.proxy?.name || t('admin.accounts.directConnection')
  return groups.length ? `${groups.join(', ')} · ${proxy}` : proxy
}
</script>
