<template>
  <AccountOperationDialog :show="contribution !== null" :title="title" :job="activeJob" :show-configuration="false" :width="width" @close="emit('close')">
    <p v-if="contribution && !activeJob && !available" role="status" class="mb-3 text-sm text-muted">{{ t('admin.plugins.extensionUnavailable') }}</p>
    <div :inert="!available ? true : undefined">
    <PluginFrame v-if="contribution && !activeJob" :plugin-id="contribution.plugin_id" :title="title"
      :admission="admission"
      :expected-package="contribution.package_sha256" :origin-view="capturedView"
      :context="{ ...capturedContext, contribution_id: contribution.id, operation: contribution.action, account_id: capturedAccountID, account_ids: capturedIDs, mode }"
      @job="acceptJob" @saved="registry.refresh" @event="(name, payload) => emit('event', name, payload)" />
    </div>
  </AccountOperationDialog>
</template>

<script setup lang="ts">
import { computed, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PluginContribution } from '@/api/admin/plugins'
import type { AccountJob } from '@/api/admin/accountJobs'
import { usePluginExtensions } from '@/stores/pluginExtensions'
import AccountOperationDialog from '@/components/admin/account-jobs/AccountOperationDialog.vue'
import PluginFrame from './PluginFrame.vue'
import { useAccountSelectionMetadata, type AccountSelectionIdentity } from '@/composables/useAccountSelectionMetadata'
import { contributionAdmission } from './contributionAdmission'
import { narrowAccountViewSelection, useAccountViewContext, type CapturedAccountView } from '@/composables/useAccountViewContext'
import { publicPluginContext, resolveContribution } from './accountView'
const props = withDefaults(defineProps<{ contribution: PluginContribution | null; accountId?: number; accountIds?: number[]; accounts?: AccountSelectionIdentity[]; mode?: string; context?: Record<string, unknown>; originView?: CapturedAccountView | null; launchKey?: number; width?: 'narrow' | 'normal' | 'wide' | 'extra-wide' | 'full' }>(), { accountIds: () => [], accounts: () => [], mode: 'account.actions', context: () => ({}), width: 'normal' })
const emit = defineEmits<{ close: []; job: [job: AccountJob]; event: [name: string, payload: unknown] }>()
const { locale, t } = useI18n()
const registry = usePluginExtensions()
const activeJob = ref<AccountJob | null>(null)
const viewController = useAccountViewContext()
const capturedView = shallowRef<CapturedAccountView | null>()
const capturedIDs = ref<number[]>([])
const capturedAccountID = ref<number>()
const capturedAccounts = ref<AccountSelectionIdentity[]>([])
const capturedContext = shallowRef<Record<string, unknown>>({})
const captureFailed = ref(false)
watch(() => [props.contribution?.plugin_id, props.contribution?.id, props.contribution?.package_sha256, props.launchKey], () => {
  if (!props.contribution) return
  capturedAccountID.value = props.accountId
  capturedIDs.value = props.accountId ? [props.accountId] : [...props.accountIds]
  capturedAccounts.value = props.accounts.map(account => ({ id: account.id, platform: account.platform, type: account.type, status: account.status, parent_account_id: account.parent_account_id }))
  capturedContext.value = publicPluginContext(props.context)
  captureFailed.value = false
  try {
    capturedView.value = props.originView === null ? null : narrowAccountViewSelection(props.originView || viewController?.capture(), capturedIDs.value)
  } catch { captureFailed.value = true }
}, { immediate: true, flush: 'sync' })
const targetIds = computed(() => props.contribution ? capturedIDs.value : [])
const selection = useAccountSelectionMetadata(targetIds, capturedAccounts, () => capturedView.value || undefined)
const admission = computed(() => {
  if (captureFailed.value) return { allowed: false }
  try { capturedView.value?.assertCurrent() } catch { return { allowed: false } }
  const item = props.contribution && resolveContribution(registry.items, { pluginId: props.contribution.plugin_id, id: props.contribution.id, packageSHA: props.contribution.package_sha256 })
  return item ? contributionAdmission(item, { accountIds: targetIds.value, accounts: selection.selectedAccounts.value }) : { allowed: false }
})
const available = computed(() => admission.value.allowed)
const title = computed(() => props.contribution?.label[locale?.value || 'zh'] || props.contribution?.label.zh || props.contribution?.label.en || '')
watch(() => [props.contribution?.plugin_id, props.contribution?.id, props.launchKey], () => { activeJob.value = null })
function acceptJob(job: AccountJob) { activeJob.value = job; emit('job', job) }
</script>
