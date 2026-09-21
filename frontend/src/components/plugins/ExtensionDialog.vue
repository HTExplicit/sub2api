<template>
  <AccountOperationDialog :show="contribution !== null" :title="title" :job="activeJob" :show-configuration="false" :width="width" @close="emit('close')">
    <p v-if="contribution && !activeJob && !available" role="status" class="mb-3 text-sm text-muted">{{ t('admin.plugins.extensionUnavailable') }}</p>
    <div :inert="!available ? true : undefined">
    <PluginFrame v-if="contribution && !activeJob" :plugin-id="contribution.plugin_id" :title="title"
      :admission="admission"
      :context="{ ...context, contribution_id: contribution.id, operation: contribution.action, account_id: accountId, account_ids: accountIds, mode }"
      @job="acceptJob" @saved="registry.refresh" @event="(name, payload) => emit('event', name, payload)" />
    </div>
  </AccountOperationDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PluginContribution } from '@/api/admin/plugins'
import type { AccountJob } from '@/api/admin/accountJobs'
import { usePluginExtensions } from '@/stores/pluginExtensions'
import AccountOperationDialog from '@/components/admin/account-jobs/AccountOperationDialog.vue'
import PluginFrame from './PluginFrame.vue'
import { useAccountSelectionMetadata, type AccountSelectionIdentity } from '@/composables/useAccountSelectionMetadata'
import { contributionAdmission } from './contributionAdmission'
const props = withDefaults(defineProps<{ contribution: PluginContribution | null; accountId?: number; accountIds?: number[]; accounts?: AccountSelectionIdentity[]; mode?: string; context?: Record<string, unknown>; width?: 'narrow' | 'normal' | 'wide' | 'extra-wide' | 'full' }>(), { accountIds: () => [], accounts: () => [], mode: 'account.actions', context: () => ({}), width: 'normal' })
const emit = defineEmits<{ close: []; job: [job: AccountJob]; event: [name: string, payload: unknown] }>()
const { locale, t } = useI18n()
const registry = usePluginExtensions()
const activeJob = ref<AccountJob | null>(null)
const targetIds = computed(() => props.contribution ? (props.accountId ? [props.accountId] : props.accountIds) : [])
const selection = useAccountSelectionMetadata(targetIds, computed(() => props.accounts))
const admission = computed(() => {
  const item = registry.items.find(item => item.plugin_id === props.contribution?.plugin_id && item.id === props.contribution?.id)
  return item ? contributionAdmission(item, { accountIds: targetIds.value, accounts: selection.selectedAccounts.value }) : { allowed: false }
})
const available = computed(() => admission.value.allowed)
const title = computed(() => props.contribution?.label[locale?.value || 'zh'] || props.contribution?.label.zh || props.contribution?.label.en || '')
watch(() => [props.contribution?.plugin_id, props.contribution?.id, props.accountId, props.accountIds.join(',')], () => { activeJob.value = null })
function acceptJob(job: AccountJob) { activeJob.value = job; emit('job', job) }
</script>
