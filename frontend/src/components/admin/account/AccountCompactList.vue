<template>
  <div ref="scrollElement" class="min-h-0 flex-1 overflow-y-auto" data-test="account-compact-list">
    <div v-if="loading" class="space-y-px">
      <div v-for="index in 6" :key="index" class="h-16 animate-pulse border-b border-gray-100 bg-gray-50/70 dark:border-dark-700 dark:bg-dark-800" />
    </div>
    <div v-else-if="accounts.length === 0" class="flex min-h-52 flex-col items-center justify-center text-gray-400">
      <Icon name="inbox" size="xl" />
      <span class="mt-2 text-sm">{{ t('empty.noData') }}</span>
    </div>
    <div v-else :style="containerStyle" role="list" :aria-owns="orderedSlotIds">
    <div v-if="spacerStyle" data-account-virtual-spacer aria-hidden="true" :style="spacerStyle" />
    <div v-for="slot in renderSlots" :key="slot.key" :id="slotId(slot.key)"
      :style="[slotStyle(slot.row), reusePresentation && slot.row ? { willChange: 'transform' } : undefined]"
      role="listitem" :aria-posinset="slot.row ? slot.row.index + 1 : undefined"
      :aria-setsize="slot.row ? accounts.length : undefined"
      :aria-hidden="slot.row ? undefined : true" :inert="slot.row ? undefined : true">
    <div v-for="{ accounts: [account], index, key } in slot.row ? [slot.row] : []" :key="reusePresentation ? slot.key : key" :data-account-id="account.id"
      :ref="measureRow"
      :data-account-virtual-index="index"
      class="account-compact-row grid w-full grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1.5 border-b border-gray-100 px-3 py-2.5 text-left transition-colors hover:bg-gray-50 dark:border-dark-700 dark:hover:bg-dark-800 lg:grid-cols-[auto_minmax(12rem,1.2fr)_minmax(8rem,0.65fr)_minmax(16rem,1fr)_minmax(12rem,0.9fr)_auto] lg:gap-y-0"
      :class="[selectedSet.has(account.id) ? 'border-l-2 border-l-primary-500 bg-raised' : '', { 'border-b-0': index === accounts.length - 1 }]"
      @vue:before-update="onRowBeforeUpdate"
      @pointerdown.capture="rememberPointerAccount($event, account.id)"
      @click.capture="guardPointerClick($event, account.id)"
      @click="emit('rowClick', account)"
    >
      <AccountSelectionCheckbox :checked="selectedSet.has(account.id)"
          :label="t('admin.accounts.selectAccount', { name: account.name })"
          @change="emit('toggle', account.id)" />

      <div class="min-w-0">
        <div class="flex min-w-0 items-center gap-2">
          <button type="button" class="truncate text-left text-sm font-semibold text-gray-900 dark:text-white" @click.stop="emit('rowClick', account)">{{ account.name }}</button>
          <span class="shrink-0 font-mono text-[10px] text-gray-400">#{{ account.id }}</span>
        </div>
        <div class="mt-0.5 truncate text-xs text-gray-500 dark:text-dark-300">{{ displayEmail(account) || '-' }}</div>
        <div class="mt-1 flex flex-wrap gap-1 lg:hidden">
          <AccountIdentityBadges :account="account" />
        </div>
      </div>

      <div class="hidden min-w-0 lg:block">
        <AccountIdentityBadges :account="account" />
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
          :batched-usage="batchedUsage?.[String(account.id)] ?? null"
          :batched-usage-error="batchedUsageErrors?.[String(account.id)] ?? null"
          :batched-usage-loading="batchedUsageLoading?.[String(account.id)] === true"
          :request-batched-usage="requestBatchedUsage"
          variant="compact"
          read-only
        />
        <AccountCapacityCell :account="account" compact class="mt-1" />
      </div>

      <div class="hidden min-w-0 lg:block">
        <div class="text-[10px] font-medium uppercase text-gray-400">{{ t('admin.accounts.classification') }}</div>
        <div class="flex items-center gap-2">
          <AccountStatusIndicator :account="account" @show-temp-unsched="emit('showTempUnsched', account)" />
          <span class="text-xs text-gray-400">{{ account.management_folder?.name || t('admin.accounts.folderUncategorized') }}</span>
        </div>
        <div class="mt-1 truncate text-xs text-gray-500 dark:text-dark-300">
          <span class="mr-1 text-[10px] font-medium uppercase text-gray-400">{{ t('admin.accounts.routing') }}</span><AccountGroupsCell :groups="account.groups" :reset-key="account.id" /><span>{{ account.proxy?.name || t('admin.accounts.directConnection') }}</span>
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
    </div>
    </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, toRef, useId, type VNode } from 'vue'
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
const { renderRows, renderSlots, slotWindow, windowScrolling, containerStyle, spacerStyle, slotStyle, measureRow } = useAccountVirtualRows(
  toRef(props, 'accounts'), scrollElement, ref(1), 112, 0, true,
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
  return rows.length > 0 ? { start: rows[0].index, end: rows[rows.length - 1].index } : null
})
useAccountUsagePrefetch(toRef(props, 'accounts'), usagePrefetchRange, reusePresentation, toRef(props, 'requestBatchedUsage'))

function onRowBeforeUpdate(vnode: VNode, previous: VNode | null) {
  if (vnode.props?.['data-account-id'] === previous?.props?.['data-account-id']) return
  const active = document.activeElement
  if (active instanceof HTMLElement && (vnode.el as HTMLElement).contains(active)) active.blur()
}

const pointerAccounts = new WeakMap<HTMLElement, number>()
function rememberPointerAccount(event: PointerEvent, accountId: number) {
  pointerAccounts.set(event.currentTarget as HTMLElement, accountId)
}

function guardPointerClick(event: MouseEvent, accountId: number) {
  const row = event.currentTarget as HTMLElement
  const pressedAccount = pointerAccounts.get(row)
  pointerAccounts.delete(row)
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
