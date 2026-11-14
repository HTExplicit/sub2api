<template>
  <div ref="scrollElement" data-test="account-card-grid" class="min-h-0 flex-1 overflow-y-auto p-0.5">
    <div v-if="loading" class="grid grid-cols-1 gap-3 lg:grid-cols-2 2xl:grid-cols-3">
      <div v-for="index in 6" :key="index" class="h-52 animate-pulse rounded-md border border-gray-200 bg-gray-50 dark:border-dark-700 dark:bg-dark-800" />
    </div>
    <div v-else-if="accounts.length === 0" class="flex min-h-52 flex-col items-center justify-center text-gray-400">
      <Icon name="inbox" size="xl" />
      <span class="mt-2 text-sm">{{ t('empty.noData') }}</span>
    </div>
    <div v-else class="flex flex-col gap-3" :style="containerStyle" role="list" :aria-owns="orderedSlotIds">
      <div v-if="spacerStyle" data-account-virtual-spacer aria-hidden="true" :style="spacerStyle" />
      <div v-for="slot in renderSlots" :key="slot.key" :id="slotId(slot.key)"
        :ref="slot.row ? measureRow : undefined" :data-account-virtual-index="slot.row?.index"
        class="grid grid-cols-1 gap-3 lg:grid-cols-2 2xl:grid-cols-3" :style="slotStyle(slot.row)"
        role="listitem" :aria-posinset="slot.row ? slot.row.index + 1 : undefined"
        :aria-setsize="slot.row ? Math.ceil(accounts.length / columns) : undefined"
        :aria-hidden="slot.row ? undefined : true" :inert="slot.row ? undefined : true">
      <article
        v-for="(account, column) in slot.row?.accounts ?? []"
        :key="reusePresentation ? column : account.id"
        :data-account-id="account.id"
        class="cursor-pointer rounded-md border bg-white p-4 transition-colors hover:border-gray-300 hover:bg-gray-50/60 dark:bg-dark-900 dark:hover:border-dark-500 dark:hover:bg-dark-800/70"
        :class="selectedSet.has(account.id) ? 'border-l-2 border-l-primary-500' : ''"
        @vue:before-update="onCardBeforeUpdate"
        @pointerdown.capture="rememberPointerAccount($event, account.id)"
        @click.capture="guardPointerClick($event, account.id)"
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

        <div class="mt-3 border-t border-gray-100 pt-3 dark:border-dark-700" data-test="account-card-usage">
          <AccountUsageCell
            :key="reusePresentation ? 'managed-list' : account.id"
            :account="account"
            :today-stats="todayStats[String(account.id)] ?? null"
            :today-stats-loading="todayStatsLoading"
            :today-stats-error="todayStatsError"
            :today-stats-updated-at="todayStatsUpdatedAt"
            :manual-refresh-token="manualRefreshToken"
            :status-now="statusNow"
            :batched-usage="batchedUsage?.[String(account.id)] ?? null"
            :batched-usage-error="batchedUsageErrors?.[String(account.id)] ?? null"
            :batched-usage-loading="batchedUsageLoading?.[String(account.id)] === true"
            :request-batched-usage="requestBatchedUsage"
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
            <AccountGroupsCell class="mt-1" :groups="account.groups" :reset-key="account.id" />
            <div class="mt-1 truncate text-xs text-gray-500 dark:text-dark-300">
              {{ account.proxy?.name || t('admin.accounts.directConnection') }}
            </div>
          </div>
        </div>

      </article>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, toRef, useId, type VNode } from 'vue'
import { useMediaQuery } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import AccountCapacityCell from '@/components/account/AccountCapacityCell.vue'
import AccountStatusIndicator from '@/components/account/AccountStatusIndicator.vue'
import AccountUsageCell from '@/components/account/AccountUsageCell.vue'
import AccountIdentityBadges from '@/components/account/AccountIdentityBadges.vue'
import AccountSelectionCheckbox from '@/components/account/AccountSelectionCheckbox.vue'
import AccountGroupsCell from '@/components/account/AccountGroupsCell.vue'
import Icon from '@/components/icons/Icon.vue'
import type { Account, AccountUsageInfo, WindowStats } from '@/types'
import { useAccountVirtualRows } from '@/composables/useAccountVirtualRows'
import { useAccountUsagePrefetch } from '@/composables/useAccountUsagePrefetch'

const props = defineProps<{
  accounts: Account[]
  loading: boolean
  selectedIds: number[]
  todayStats: Record<string, WindowStats>
  todayStatsLoading: boolean
  todayStatsError: boolean
  todayStatsUpdatedAt: number | null
  manualRefreshToken: number
  statusNow: number
  batchedUsage?: Record<string, AccountUsageInfo | null>
  batchedUsageErrors?: Record<string, string | null>
  batchedUsageLoading?: Record<string, boolean>
  requestBatchedUsage?: ((account: Account, options?: { force?: boolean }) => void) | null
}>()

const emit = defineEmits<{
  rowClick: [account: Account]
  toggle: [id: number]
  edit: [account: Account]
  more: [account: Account, event: MouseEvent]
  showTempUnsched: [account: Account]
}>()

const { t } = useI18n()
const selectedSet = computed(() => new Set(props.selectedIds))
const scrollElement = ref<HTMLElement | null>(null)
const wide = useMediaQuery('(min-width: 1536px)')
const desktop = useMediaQuery('(min-width: 1024px)')
const columns = computed(() => wide.value ? 3 : desktop.value ? 2 : 1)
const { renderRows, renderSlots, slotWindow, windowScrolling, containerStyle, spacerStyle, slotStyle, measureRow } = useAccountVirtualRows(
  toRef(props, 'accounts'), scrollElement, columns, 360, 12, true,
)
const listId = useId()
const slotId = (key: string) => `${listId}-${key}`
const orderedSlotIds = computed(() => slotWindow.value
  ? renderSlots.value.flatMap(slot => slot.row ? [{ id: slotId(slot.key), index: slot.row.index }] : [])
    .sort((a, b) => a.index - b.index).map(slot => slot.id).join(' ')
  : undefined,
)
const reusePresentation = computed(() => slotWindow.value && !windowScrolling.value &&
  typeof props.requestBatchedUsage === 'function')
const usagePrefetchRange = computed(() => {
  const rows = renderRows.value
  return rows.length > 0 ? {
    start: rows[0].index * columns.value,
    end: Math.min((rows[rows.length - 1].index + 1) * columns.value, props.accounts.length) - 1,
  } : null
})
const usagePrefetchEnabled = computed(() => props.accounts.length > 50 && desktop.value &&
  typeof props.requestBatchedUsage === 'function')
useAccountUsagePrefetch(toRef(props, 'accounts'), usagePrefetchRange, usagePrefetchEnabled, toRef(props, 'requestBatchedUsage'))

function onCardBeforeUpdate(vnode: VNode, previous: VNode | null) {
  if (vnode.props?.['data-account-id'] === previous?.props?.['data-account-id']) return
  const active = document.activeElement
  if (active instanceof HTMLElement && (vnode.el as HTMLElement).contains(active)) active.blur()
}

const pointerAccounts = new WeakMap<HTMLElement, number>()
function rememberPointerAccount(event: PointerEvent, accountId: number) {
  pointerAccounts.set(event.currentTarget as HTMLElement, accountId)
}

function guardPointerClick(event: MouseEvent, accountId: number) {
  const card = event.currentTarget as HTMLElement
  const pressedAccount = pointerAccounts.get(card)
  pointerAccounts.delete(card)
  // A recycled control must not apply a pointer press that began on a different account.
  if (event.detail > 0 && pressedAccount !== undefined && pressedAccount !== accountId) {
    event.preventDefault()
    event.stopImmediatePropagation()
  }
}

const displayEmail = (account: Account) => String(
  account.extra?.email_address || account.extra?.email || account.credentials?.email || account.parent_email || ''
)
</script>
