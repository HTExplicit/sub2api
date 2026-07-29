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
        :class="selectedIds.includes(account.id) ? 'border-primary-300 ring-1 ring-primary-200 dark:border-primary-700 dark:ring-primary-900' : 'border-gray-200 dark:border-dark-700'"
        @click="emit('rowClick', account)"
      >
        <div class="flex items-start gap-3">
          <input
            type="checkbox"
            class="mt-0.5 h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
            :checked="selectedIds.includes(account.id)"
            :aria-label="t('admin.accounts.selectAccount', { name: account.name })"
            @click.stop
            @change.stop="emit('toggle', account.id)"
          />
          <div class="min-w-0 flex-1">
            <div class="flex min-w-0 items-center gap-2">
              <h3 class="truncate text-sm font-semibold text-gray-900 dark:text-white">{{ account.name }}</h3>
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
          <PlatformTypeBadge
            :platform="account.platform"
            :type="account.type"
            :auth-mode="authMode(account)"
            :plan-type="planType(account)"
          />
          <AccountStatusIndicator :account="account" @show-temp-unsched="emit('showTempUnsched', account)" />
        </div>

        <div class="mt-4 grid grid-cols-2 gap-x-4 gap-y-3 border-t border-gray-100 pt-3 dark:border-dark-700">
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
            <div class="mt-1 truncate text-xs text-gray-700 dark:text-gray-200">{{ groupSummary(account) }}</div>
            <div class="mt-1 truncate text-xs text-gray-500 dark:text-dark-300">
              {{ account.proxy?.name || t('admin.accounts.directConnection') }}
            </div>
          </div>
        </div>

        <div class="mt-3 flex items-end justify-between gap-3 border-t border-gray-100 pt-3 dark:border-dark-700">
          <AccountCapacityCell :account="account" />
          <div class="text-right text-xs text-gray-500 dark:text-dark-300">
            <div>{{ t('admin.accounts.todayRequestsShort', { count: todayStats[String(account.id)]?.requests || 0 }) }}</div>
            <div class="mt-0.5 font-mono">${{ Number(todayStats[String(account.id)]?.cost || 0).toFixed(2) }}</div>
          </div>
        </div>
      </article>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import AccountCapacityCell from '@/components/account/AccountCapacityCell.vue'
import AccountStatusIndicator from '@/components/account/AccountStatusIndicator.vue'
import PlatformTypeBadge from '@/components/common/PlatformTypeBadge.vue'
import Icon from '@/components/icons/Icon.vue'
import type { Account, WindowStats } from '@/types'

defineProps<{
  accounts: Account[]
  loading: boolean
  selectedIds: number[]
  todayStats: Record<string, WindowStats>
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
const groupSummary = (account: Account) => account.groups?.map(group => group.name).filter(Boolean).join(', ')
  || t('admin.accounts.ungroupedGroup')
</script>
