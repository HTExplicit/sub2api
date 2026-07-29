<template>
  <Teleport to="body">
    <Transition
      enter-active-class="transition-opacity duration-200"
      enter-from-class="opacity-0"
      leave-active-class="transition-opacity duration-150"
      leave-to-class="opacity-0"
    >
      <div
        v-if="account"
        class="fixed inset-0 z-[10000] bg-black/35"
        aria-hidden="true"
        @click="emit('close')"
      />
    </Transition>
    <Transition
      enter-active-class="transition-transform duration-200 ease-out"
      enter-from-class="translate-x-full"
      leave-active-class="transition-transform duration-150 ease-in"
      leave-to-class="translate-x-full"
    >
      <aside
        v-if="account"
        class="fixed inset-y-0 right-0 z-[10001] flex w-full max-w-xl flex-col border-l border-gray-200 bg-white shadow-2xl dark:border-dark-700 dark:bg-dark-900"
        role="dialog"
        aria-modal="true"
        :aria-label="t('admin.accounts.detailsTitle')"
        data-test="account-details-drawer"
        @keydown.esc="emit('close')"
      >
        <header class="flex items-start gap-3 border-b border-gray-200 px-4 py-4 dark:border-dark-700 sm:px-5">
          <div class="min-w-0 flex-1">
            <div class="flex min-w-0 items-center gap-2">
              <h2 class="truncate text-base font-semibold text-gray-900 dark:text-white">{{ account.name }}</h2>
              <span class="shrink-0 font-mono text-xs text-gray-400">#{{ account.id }}</span>
            </div>
            <p class="mt-1 truncate text-xs text-gray-500 dark:text-dark-300">{{ displayEmail || '-' }}</p>
          </div>
          <button type="button" class="icon-button" :title="t('common.edit')" @click="emit('edit', account)">
            <Icon name="edit" size="sm" />
          </button>
          <button type="button" class="icon-button" :title="t('common.close')" @click="emit('close')">
            <Icon name="x" size="sm" />
          </button>
        </header>

        <div class="min-h-0 flex-1 overflow-y-auto">
          <section class="border-b border-gray-100 px-4 py-4 dark:border-dark-700 sm:px-5">
            <div class="flex flex-wrap items-center gap-2">
              <PlatformTypeBadge
                :platform="account.platform"
                :type="account.type"
                :auth-mode="authMode"
                :plan-type="planType"
                :privacy-mode="String(account.extra?.privacy_mode || account.parent_privacy_mode || '')"
                :subscription-expires-at="String(account.credentials?.subscription_expires_at || account.parent_subscription_expires_at || '')"
              />
              <AccountStatusIndicator :account="account" @show-temp-unsched="emit('showTempUnsched', account)" />
              <span
                class="inline-flex items-center rounded px-2 py-0.5 text-xs font-medium"
                :class="account.schedulable
                  ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-300'
                  : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'"
              >
                {{ account.schedulable ? t('admin.accounts.schedulableEnabled') : t('admin.accounts.schedulableDisabled') }}
              </span>
            </div>
            <p v-if="account.error_message" class="mt-3 whitespace-pre-wrap rounded bg-red-50 px-3 py-2 text-xs text-red-700 dark:bg-red-900/20 dark:text-red-300">
              {{ account.error_message }}
            </p>
            <p v-if="account.temp_unschedulable_reason" class="mt-2 whitespace-pre-wrap text-xs text-amber-700 dark:text-amber-300">
              {{ account.temp_unschedulable_reason }}
            </p>
          </section>

          <section class="border-b border-gray-100 px-4 py-4 dark:border-dark-700 sm:px-5">
            <div class="mb-3 flex items-center justify-between gap-3">
              <h3 class="text-xs font-semibold uppercase text-gray-500 dark:text-dark-300">{{ t('admin.accounts.classification') }}</h3>
              <button
                type="button"
                class="btn btn-primary px-2.5 py-1.5 text-xs"
                :disabled="taxonomySaving || !taxonomyDirty"
                @click="saveTaxonomy"
              >
                {{ taxonomySaving ? t('common.saving') : t('common.save') }}
              </button>
            </div>
            <label class="block text-xs font-medium text-gray-500 dark:text-dark-300">
              {{ t('admin.accounts.folder') }}
              <select v-model="folderID" class="input mt-1 h-9 py-1.5 text-sm">
                <option value="">{{ t('admin.accounts.folderUncategorized') }}</option>
                <option v-for="folder in folders" :key="folder.id" :value="String(folder.id)">{{ folder.name }}</option>
              </select>
            </label>
            <div class="mt-3">
              <div class="text-xs font-medium text-gray-500 dark:text-dark-300">{{ t('admin.accounts.tags') }}</div>
              <div class="mt-1.5 flex flex-wrap gap-1.5">
                <label
                  v-for="tag in tags"
                  :key="tag.id"
                  class="inline-flex cursor-pointer items-center gap-1.5 rounded border px-2 py-1 text-xs transition-colors"
                  :class="tagIDs.includes(tag.id)
                    ? 'border-primary-300 bg-primary-50 text-primary-700 dark:border-primary-700 dark:bg-primary-900/20 dark:text-primary-300'
                    : 'border-gray-200 text-gray-600 hover:bg-gray-50 dark:border-dark-700 dark:text-gray-300 dark:hover:bg-dark-800'"
                >
                  <input v-model="tagIDs" type="checkbox" class="sr-only" :value="tag.id" />
                  <Icon v-if="tagIDs.includes(tag.id)" name="check" size="xs" />
                  <span>{{ tag.name }}</span>
                </label>
                <span v-if="tags.length === 0" class="text-xs text-gray-400">{{ t('admin.accounts.noTags') }}</span>
              </div>
            </div>
          </section>

          <section class="border-b border-gray-100 px-4 py-4 dark:border-dark-700 sm:px-5">
            <h3 class="mb-3 text-xs font-semibold uppercase text-gray-500 dark:text-dark-300">{{ t('admin.accounts.capacityAndUsage') }}</h3>
            <div class="flex items-start justify-between gap-4">
              <AccountCapacityCell :account="account" />
              <div class="text-right text-xs text-gray-500 dark:text-dark-300">
                <div>{{ t('admin.accounts.todayRequestsShort', { count: todayStats?.requests || 0 }) }}</div>
                <div class="mt-1 font-mono">${{ Number(todayStats?.cost || 0).toFixed(2) }}</div>
                <div class="mt-1 font-mono">{{ Number(todayStats?.tokens || 0).toLocaleString() }} tokens</div>
              </div>
            </div>
            <div class="mt-4 border-t border-gray-100 pt-4 dark:border-dark-700">
              <AccountUsageCell
                :account="account"
                :today-stats="todayStats"
                :today-stats-loading="todayStatsLoading"
                :manual-refresh-token="manualRefreshToken"
              />
            </div>
          </section>

          <section class="border-b border-gray-100 px-4 py-4 dark:border-dark-700 sm:px-5">
            <h3 class="mb-3 text-xs font-semibold uppercase text-gray-500 dark:text-dark-300">{{ t('admin.accounts.routingAndScheduling') }}</h3>
            <dl class="grid grid-cols-[minmax(7rem,0.8fr)_minmax(0,1.4fr)] gap-x-4 gap-y-2 text-sm">
              <dt class="text-gray-500 dark:text-dark-300">{{ t('admin.accounts.columns.groups') }}</dt>
              <dd class="min-w-0 text-right text-gray-800 dark:text-gray-100">{{ groupNames || t('admin.accounts.ungroupedGroup') }}</dd>
              <dt class="text-gray-500 dark:text-dark-300">{{ t('admin.accounts.columns.proxy') }}</dt>
              <dd class="truncate text-right text-gray-800 dark:text-gray-100">{{ account.proxy?.name || t('admin.accounts.directConnection') }}</dd>
              <dt class="text-gray-500 dark:text-dark-300">{{ t('admin.accounts.columns.priority') }}</dt>
              <dd class="font-mono text-right text-gray-800 dark:text-gray-100">{{ account.priority }}</dd>
              <dt class="text-gray-500 dark:text-dark-300">{{ t('admin.accounts.columns.billingRateMultiplier') }}</dt>
              <dd class="font-mono text-right text-gray-800 dark:text-gray-100">{{ Number(account.rate_multiplier ?? 1).toFixed(2) }}x</dd>
              <dt class="text-gray-500 dark:text-dark-300">{{ t('admin.accounts.concurrency') }}</dt>
              <dd class="font-mono text-right text-gray-800 dark:text-gray-100">{{ account.current_concurrency || 0 }} / {{ account.concurrency }}</dd>
              <dt class="text-gray-500 dark:text-dark-300">{{ t('admin.accounts.loadFactor') }}</dt>
              <dd class="font-mono text-right text-gray-800 dark:text-gray-100">{{ account.load_factor ?? '-' }}</dd>
            </dl>

            <div v-if="schedulerRows.length" class="mt-4 border-t border-gray-100 pt-3 dark:border-dark-700">
              <div class="mb-2 text-xs font-medium text-gray-500 dark:text-dark-300">{{ t('admin.accounts.columns.schedulerScore') }}</div>
              <div v-for="score in schedulerRows" :key="String(score.group_id)" class="flex items-center justify-between gap-3 py-1 font-mono text-xs text-gray-700 dark:text-gray-200">
                <span class="truncate">{{ score.group_name || t('admin.accounts.schedulerScore.ungrouped') }}</span>
                <span>{{ formatScore(score.base_score) }} / {{ score.sticky_score_infinity ? '+∞' : formatScore(score.sticky_score) }}</span>
              </div>
            </div>
          </section>

          <section class="border-b border-gray-100 px-4 py-4 dark:border-dark-700 sm:px-5">
            <h3 class="mb-3 text-xs font-semibold uppercase text-gray-500 dark:text-dark-300">{{ t('admin.accounts.detailsAndTime') }}</h3>
            <dl class="grid grid-cols-[minmax(7rem,0.8fr)_minmax(0,1.4fr)] gap-x-4 gap-y-2 text-sm">
              <dt class="text-gray-500 dark:text-dark-300">{{ t('admin.accounts.columns.lastUsed') }}</dt>
              <dd class="text-right text-gray-800 dark:text-gray-100">{{ formatValue(account.last_used_at) }}</dd>
              <dt class="text-gray-500 dark:text-dark-300">{{ t('admin.accounts.columns.createdAt') }}</dt>
              <dd class="text-right text-gray-800 dark:text-gray-100">{{ formatValue(account.created_at) }}</dd>
              <dt class="text-gray-500 dark:text-dark-300">{{ t('admin.accounts.columns.expiresAt') }}</dt>
              <dd class="text-right text-gray-800 dark:text-gray-100">{{ expiresAt }}</dd>
              <dt class="text-gray-500 dark:text-dark-300">{{ t('admin.accounts.rateLimitResetAt') }}</dt>
              <dd class="text-right text-gray-800 dark:text-gray-100">{{ formatValue(account.rate_limit_reset_at) }}</dd>
            </dl>
            <div class="mt-4 border-t border-gray-100 pt-3 dark:border-dark-700">
              <div class="text-xs font-medium text-gray-500 dark:text-dark-300">{{ t('admin.accounts.notes') }}</div>
              <p class="mt-1 whitespace-pre-wrap text-sm text-gray-700 dark:text-gray-200">{{ account.notes || '-' }}</p>
            </div>
          </section>
        </div>
      </aside>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import AccountCapacityCell from '@/components/account/AccountCapacityCell.vue'
import AccountStatusIndicator from '@/components/account/AccountStatusIndicator.vue'
import AccountUsageCell from '@/components/account/AccountUsageCell.vue'
import PlatformTypeBadge from '@/components/common/PlatformTypeBadge.vue'
import Icon from '@/components/icons/Icon.vue'
import { formatDateTime } from '@/utils/format'
import type {
  Account,
  AccountManagementFolder,
  AccountManagementTag,
  AccountSchedulerGroupScore,
  WindowStats
} from '@/types'

const props = defineProps<{
  account: Account | null
  folders: AccountManagementFolder[]
  tags: AccountManagementTag[]
  todayStats: WindowStats | null
  todayStatsLoading: boolean
  manualRefreshToken: number
}>()

const emit = defineEmits<{
  close: []
  edit: [account: Account]
  updated: [account: Account]
  showTempUnsched: [account: Account]
}>()

const { t } = useI18n()
const appStore = useAppStore()
const folderID = ref('')
const tagIDs = ref<number[]>([])
const taxonomySaving = ref(false)

const resetTaxonomy = () => {
  folderID.value = props.account?.management_folder ? String(props.account.management_folder.id) : ''
  tagIDs.value = (props.account?.tags || []).map(tag => tag.id)
}

watch(() => props.account?.id, resetTaxonomy, { immediate: true })

const taxonomyDirty = computed(() => {
  const originalFolder = props.account?.management_folder ? String(props.account.management_folder.id) : ''
  const originalTags = (props.account?.tags || []).map(tag => tag.id).sort((a, b) => a - b)
  const nextTags = [...tagIDs.value].sort((a, b) => a - b)
  return folderID.value !== originalFolder || JSON.stringify(originalTags) !== JSON.stringify(nextTags)
})

const displayEmail = computed(() => String(
  props.account?.extra?.email_address || props.account?.extra?.email || props.account?.credentials?.email || props.account?.parent_email || ''
))
const authMode = computed(() => typeof props.account?.credentials?.auth_mode === 'string'
  ? props.account.credentials.auth_mode
  : undefined)
const planType = computed(() => String(
  (props.account?.extra?.grok_billing_snapshot as Record<string, unknown> | undefined)?.plan ||
  (props.account?.extra?.grok_quota_snapshot as Record<string, unknown> | undefined)?.subscription_tier ||
  props.account?.credentials?.subscription_tier || props.account?.extra?.subscription_tier || props.account?.credentials?.plan_type || ''
))
const groupNames = computed(() => props.account?.groups?.map(group => group.name).filter(Boolean).join(', ') || '')
const schedulerRows = computed<AccountSchedulerGroupScore[]>(() => {
  if (props.account?.scheduler_scores?.length) return props.account.scheduler_scores
  return props.account?.scheduler_score ? [{ group_id: null, ...props.account.scheduler_score }] : []
})
const expiresAt = computed(() => {
  if (!props.account?.expires_at) return '-'
  return formatDateTime(new Date(props.account.expires_at * 1000))
})

const formatValue = (value: string | Date | null | undefined) => value ? formatDateTime(new Date(value)) : '-'
const formatScore = (value: unknown) => {
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed.toFixed(6).replace(/\.?0+$/, '') : '-'
}

const saveTaxonomy = async () => {
  if (!props.account || taxonomySaving.value || !taxonomyDirty.value) return
  taxonomySaving.value = true
  try {
    const updated = await adminAPI.accounts.setTaxonomy(
      props.account.id,
      folderID.value ? Number(folderID.value) : null,
      tagIDs.value
    )
    emit('updated', updated)
    appStore.showSuccess(t('admin.accounts.taxonomySaved'))
  } catch (error: any) {
    appStore.showError(error?.message || t('common.unknownError'))
  } finally {
    taxonomySaving.value = false
  }
}

const handleEscape = (event: KeyboardEvent) => {
  if (event.key === 'Escape' && props.account) emit('close')
}

onMounted(() => document.addEventListener('keydown', handleEscape))
onUnmounted(() => document.removeEventListener('keydown', handleEscape))
</script>
