<template>
  <div v-if="visible.length" :class="variant === 'menu' ? 'contents' : 'flex flex-wrap items-center gap-2'" :data-extension-slot="name">
    <button v-for="item in visible" :key="`${item.plugin_id}:${item.id}`" type="button"
      :class="variant === 'menu' ? 'flex w-full items-center gap-2 px-4 py-2 text-left text-sm hover:bg-gray-100 disabled:cursor-not-allowed disabled:opacity-60 dark:hover:bg-dark-700' : 'btn btn-secondary btn-sm'"
      :disabled="!available(item) || executing === `${item.plugin_id}:${item.id}`" :title="available(item) ? label(item) : unavailableReason()" @click="open(item)">
      {{ label(item) }}
    </button>
  </div>
  <ExtensionDialog v-if="!external" :contribution="selected" :launch-key="launchKey" :account-id="account?.id" :account-ids="accountIds" :accounts="account ? [account] : accounts" :mode="name" @close="selected = null" @job="acceptJob" />
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AccountSelectionIdentity } from '@/composables/useAccountSelectionMetadata'
import type { AccountJob } from '@/api/admin/accountJobs'
import type { PluginContribution } from '@/api/admin/plugins'
import { usePluginExtensions } from '@/stores/pluginExtensions'
import ExtensionDialog from './ExtensionDialog.vue'
import { contributionAdmission } from './contributionAdmission'
import { accountMatchesPredicate, accountViewAllowsAction, resolveAccountView, resolveContribution } from './accountView'
import { callPluginResource } from './resourceClient'
import { narrowAccountViewSelection, useAccountViewContext } from '@/composables/useAccountViewContext'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { adminAPI } from '@/api/admin'
import type { Account } from '@/types'

const props = withDefaults(defineProps<{ name: string; account?: AccountSelectionIdentity; accountIds?: number[]; accounts?: AccountSelectionIdentity[]; external?: boolean; variant?: 'buttons' | 'menu' }>(), { accountIds: () => [], accounts: () => [], external: false, variant: 'buttons' })
const emit = defineEmits<{ job: [job: AccountJob]; open: [contribution: PluginContribution]; resourceComplete: [] }>()
const { t, locale } = useI18n()
const registry = usePluginExtensions()
const viewController = useAccountViewContext()
const app = useAppStore(), auth = useAuthStore()
const stepUp = useStepUp()
const executing = ref(''), launchKey = ref(0)
let disposed = false
const controllers = new Set<AbortController>()
const selected = ref<PluginContribution | null>(null)
const activeJob = ref<AccountJob | null>(null)
function acceptJob(job: AccountJob) { activeJob.value = job; emit('job', job) }
async function open(item: PluginContribution) {
  if (!available(item) || executing.value) return
  if (item.resource_action) { await executeResourceAction(item); return }
  if (props.external) emit('open', item)
  else { launchKey.value++; selected.value = item }
}
watch(() => [selected.value?.plugin_id, selected.value?.id, launchKey.value], () => { activeJob.value = null })
function label(item: PluginContribution) { return item.label[locale?.value || 'zh'] || item.label.zh || item.label.en || Object.values(item.label)[0] || t('admin.plugins.configure') }
function available(item: PluginContribution) { return viewController?.available() !== false && props.accountIds.length <= 100 && contributionAdmission(item, { account: props.account, accountIds: props.accountIds, accounts: props.accounts }).allowed }
function unavailableReason() { return props.accountIds.length > 100 ? t('admin.plugins.accountLimit') : t('admin.plugins.extensionUnavailable') }
function matches(item: PluginContribution, account: AccountSelectionIdentity) {
  if (item.resource_action && !accountMatchesPredicate(account as Account, item.resource_action.row_predicate)) return false
  const filter = item.account_filter
  return !filter || ((!filter.platforms?.length || filter.platforms.includes(account.platform)) &&
    (!filter.types?.length || filter.types.includes(account.type)) &&
    (!filter.statuses?.length || filter.statuses.includes(account.status || '')) &&
    (!filter.exclude_shadows || account.parent_account_id == null))
}
const viewOwner = computed(() => {
  try {
    const identity = viewController?.capture()?.context
    return identity ? resolveAccountView(registry.items, identity.plugin_key, identity.view_id) : undefined
  } catch { return undefined }
})
const visible = computed(() => registry.items.filter(item => {
  if (item.slot !== props.name || item.permission !== 'admin') return false
  if (props.name === 'account.actions' && !accountViewAllowsAction(item, viewOwner.value)) return false
  if (props.account) return matches(item, props.account)
  if (props.accountIds.length) {
    const known = new Map(props.accounts.map(account => [account.id, account]))
    return props.accountIds.every(id => { const account = known.get(id); return !!account && matches(item, account) })
  }
  return !item.account_filter
}))
async function executeResourceAction(item: PluginContribution) {
  const action = item.resource_action, accountID = props.account?.id, actorID = auth.user?.id
  if (!action || action.version !== 1 || action.account_source !== 'row.id' || action.account_parameter !== 'id' || action.effect !== 'refresh_current_account_view' || !accountID || !item.package_sha256) return
  const view = narrowAccountViewSelection(viewController?.capture(), [accountID])
  const controller = new AbortController()
  controllers.add(controller); executing.value = `${item.plugin_id}:${item.id}`
  const ensure = () => {
    const current = resolveContribution(registry.items, { pluginId: item.plugin_id, id: item.id, packageSHA: item.package_sha256 })
    if (disposed || auth.user?.id !== actorID || !current || !contributionAdmission(current, { account: props.account }).allowed) throw new Error(t('admin.plugins.extensionUnavailable'))
    view?.assertCurrent()
  }
  try {
    ensure()
    const catalog = await adminAPI.plugins.resources(item.plugin_id, 'admin', { view })
    ensure()
    const matches = catalog.filter(resource => resource.name === action.resource)
    const descriptor = matches.length === 1 ? matches[0] : undefined
    if (!descriptor?.available || descriptor.permission !== 'admin') throw new Error(t('admin.plugins.extensionUnavailable'))
    const operationKey = `account-action-${globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(36).slice(2)}`}`
    await stepUp.run(() => {
      ensure()
      return callPluginResource(item.plugin_id, item.package_sha256!, descriptor, { params: { id: accountID }, operation_key: operationKey }, controller.signal, actorID, { view })
    })
    ensure(); emit('resourceComplete')
  } catch (error) {
    if (!disposed && !isStepUpCancelled(error)) app.showError(error instanceof Error ? error.message : t('common.operationFailed'))
  } finally { controllers.delete(controller); executing.value = '' }
}
onBeforeUnmount(() => { disposed = true; for (const controller of controllers) controller.abort() })
onMounted(() => { if (!registry.loaded) void registry.refresh() })
</script>
