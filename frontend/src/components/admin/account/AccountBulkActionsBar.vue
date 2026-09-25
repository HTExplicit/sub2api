<template>
  <div class="mb-4 flex flex-col gap-3 rounded-none border border-line border-l-2 border-l-primary-500 bg-raised p-3 xl:flex-row xl:items-center xl:justify-between">
    <div class="flex flex-wrap items-center gap-2">
      <span v-if="allResultsSelected" class="text-sm font-medium text-ink">
        {{ t('admin.accounts.bulkActions.selectedAll', { count: selectedIds.length }) }}
      </span>
      <span v-else-if="selectedIds.length > 0" class="text-sm font-medium text-ink">
        {{ t('admin.accounts.bulkActions.selected', { count: selectedIds.length }) }}
      </span>
      <span v-else class="text-sm font-medium text-ink">
        {{ t('admin.accounts.bulkEdit.title') }}
      </span>
      <template v-if="selectedIds.length > 0">
        <button
          @click="$emit('select-page')"
          class="text-xs font-medium text-primary-700 hover:text-primary-800 dark:text-primary-300 dark:hover:text-primary-200"
        >
          {{ t('admin.accounts.bulkActions.selectCurrentPage') }}
        </button>
      </template>
      <template v-if="!allResultsSelected && totalResults > selectedIds.length">
        <span v-if="selectedIds.length > 0" class="text-gray-300 dark:text-primary-800">•</span>
        <button
          :disabled="selectingAll"
          @click="$emit('select-all-results')"
          class="text-xs font-medium text-primary-700 hover:text-primary-800 disabled:cursor-not-allowed disabled:opacity-60 dark:text-primary-300 dark:hover:text-primary-200"
        >
          {{
            selectingAll
              ? t('admin.accounts.bulkActions.selectingAll')
              : t('admin.accounts.bulkActions.selectAllResults', { count: totalResults })
          }}
        </button>
      </template>
      <template v-if="selectedIds.length > 0">
        <span class="text-gray-300 dark:text-primary-800">•</span>
        <button
          @click="$emit('clear')"
          class="text-xs font-medium text-primary-700 hover:text-primary-800 dark:text-primary-300 dark:hover:text-primary-200"
        >
          {{ t('admin.accounts.bulkActions.clear') }}
        </button>
      </template>
    </div>
    <div class="flex flex-wrap gap-2">
      <template v-if="selectedIds.length > 0">
        <button @click="$emit('delete')" class="btn btn-danger btn-sm">{{ t('admin.accounts.bulkActions.delete') }}</button>
        <CodexAccountActions :account-ids="selectedIds" :accounts="selectedAccounts" @open="openCodexOperation" />
        <button v-if="promptBindingAvailable" type="button" data-test="account-prompt-binding-bulk" class="btn btn-secondary btn-sm" @click="promptBindingOpen = true">
          {{ t('admin.systemPrompts.accountPrompts') }}
        </button>
        <button @click="$emit('reset-status')" class="btn btn-secondary btn-sm">{{ t('admin.accounts.bulkActions.resetStatus') }}</button>
        <button @click="$emit('refresh-token')" class="btn btn-secondary btn-sm">{{ t('admin.accounts.bulkActions.refreshToken') }}</button>
        <button data-test="refresh-tier" @click="$emit('refresh-tier')" class="btn btn-secondary btn-sm">{{ t('admin.accounts.bulkActions.refreshTier') }}</button>
        <button
          v-if="selectedIds.length >= 2 && selectedIds.length <= 100"
          type="button"
          data-test="duplicate-review"
          class="btn btn-secondary btn-sm"
          @click="$emit('duplicate-review')"
        >
          {{ t('admin.accounts.bulkActions.duplicateReview') }}
        </button>
        <button @click="$emit('probe-upstream-billing')" class="btn btn-secondary btn-sm">{{ t('admin.accounts.bulkActions.probeUpstreamBilling') }}</button>
        <button @click="$emit('test-connection')" class="btn btn-secondary btn-sm">{{ t('admin.accounts.bulkActions.testConnection') }}</button>
        <button @click="$emit('toggle-schedulable', true)" class="btn btn-success btn-sm">{{ t('admin.accounts.bulkActions.enableScheduling') }}</button>
        <button @click="$emit('toggle-schedulable', false)" class="btn btn-warning btn-sm">{{ t('admin.accounts.bulkActions.disableScheduling') }}</button>
        <button @click="$emit('edit-selected')" class="btn btn-primary btn-sm">{{ t('admin.accounts.bulkActions.edit') }}</button>
        <button @click="$emit('taxonomy-selected')" class="btn btn-secondary btn-sm">{{ t('admin.accounts.bulkTaxonomy.selectedAction') }}</button>
      </template>
      <button @click="$emit('edit-filtered')" class="btn btn-primary btn-sm">
        {{ t('admin.accounts.bulkEdit.submit') }}
      </button>
      <button @click="$emit('taxonomy-filtered')" class="btn btn-secondary btn-sm">
        {{ t('admin.accounts.bulkTaxonomy.filteredAction') }}
      </button>
    </div>
  </div>
  <CodexTicketOperationModal v-if="codexTarget" :show="true" :operation="codexTarget.operation" :account-ids="codexTarget.accountIds" @close="codexTarget = null" />
  <BaseDialog :show="promptBindingOpen" :title="t('admin.systemPrompts.accountPrompts')" width="normal" @close="promptBindingOpen = false">
    <AccountPromptBindingPanel v-if="promptBindingOpen" :account-ids="selectedIds" />
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, defineAsyncComponent, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { accountPromptBindingLimit, supportsAccountPromptBinding } from '@/utils/accountPromptBinding'
import type { AccountSelectionIdentity } from '@/composables/useAccountSelectionMetadata'
import CodexAccountActions from '@/components/admin/codex/CodexAccountActions.vue'
import type { CodexTicketOperation } from '@/utils/codexTickets'

const props = defineProps<{
  selectedIds: number[]
  totalResults: number
  selectingAll: boolean
  allResultsSelected: boolean
  selectedAccounts?: AccountSelectionIdentity[]
}>()

defineEmits([
  'delete',
  'edit-selected',
  'edit-filtered',
  'taxonomy-selected',
  'taxonomy-filtered',
  'clear',
  'select-page',
  'select-all-results',
  'toggle-schedulable',
  'reset-status',
  'refresh-token',
  'refresh-tier',
  'duplicate-review',
  'probe-upstream-billing',
  'test-connection'
])

const { t } = useI18n()
const AccountPromptBindingPanel = defineAsyncComponent(() => import('./AccountPromptBindingPanel.vue'))
const CodexTicketOperationModal = defineAsyncComponent(() => import('@/components/admin/codex/CodexTicketOperationModal.vue'))
const codexTarget = ref<{ operation: CodexTicketOperation; accountIds: number[] } | null>(null)
function openCodexOperation(operation: CodexTicketOperation, accountIds: number[]) {
  codexTarget.value = { operation, accountIds: [...accountIds] }
}
const promptBindingOpen = ref(false)
const promptBindingAvailable = computed(() => {
  if (!props.selectedIds.length || props.selectedIds.length > accountPromptBindingLimit) return false
  const known = new Map((props.selectedAccounts || []).map(account => [account.id, account]))
  return props.selectedIds.every(id => {
    const account = known.get(id)
    return !!account && supportsAccountPromptBinding(account)
  })
})
</script>
