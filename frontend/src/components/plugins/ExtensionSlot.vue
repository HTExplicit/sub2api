<template>
  <div v-if="visible.length" :class="variant === 'menu' ? 'contents' : 'flex flex-wrap items-center gap-2'" :data-extension-slot="name">
    <button v-for="item in visible" :key="`${item.plugin_id}:${item.id}`" type="button"
      :class="variant === 'menu' ? 'flex w-full items-center gap-2 px-4 py-2 text-left text-sm hover:bg-gray-100 disabled:cursor-not-allowed disabled:opacity-60 dark:hover:bg-dark-700' : 'btn btn-secondary btn-sm'"
      :disabled="!available(item)" :title="available(item) ? label(item) : unavailableReason()" @click="open(item)">
      {{ label(item) }}
    </button>
  </div>
  <ExtensionDialog v-if="!external" :contribution="selected" :account-id="account?.id" :account-ids="accountIds" :mode="name" @close="selected = null" @job="acceptJob" />
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AccountSelectionIdentity } from '@/composables/useAccountSelectionMetadata'
import type { AccountJob } from '@/api/admin/accountJobs'
import type { PluginContribution } from '@/api/admin/plugins'
import { usePluginExtensions } from '@/stores/pluginExtensions'
import ExtensionDialog from './ExtensionDialog.vue'

const props = withDefaults(defineProps<{ name: string; account?: AccountSelectionIdentity; accountIds?: number[]; accounts?: AccountSelectionIdentity[]; external?: boolean; variant?: 'buttons' | 'menu' }>(), { accountIds: () => [], accounts: () => [], external: false, variant: 'buttons' })
const emit = defineEmits<{ job: [job: AccountJob]; open: [contribution: PluginContribution] }>()
const { t, locale } = useI18n()
const registry = usePluginExtensions()
const selected = ref<PluginContribution | null>(null)
const activeJob = ref<AccountJob | null>(null)
function acceptJob(job: AccountJob) { activeJob.value = job; emit('job', job) }
function open(item: PluginContribution) { if (props.external) emit('open', item); else selected.value = item }
watch(() => selected.value?.id, () => { activeJob.value = null })
function label(item: PluginContribution) { return item.label[locale?.value || 'zh'] || item.label.zh || item.label.en || Object.values(item.label)[0] || t('admin.plugins.configure') }
function available(item: PluginContribution) { return item.available && props.accountIds.length <= 100 }
function unavailableReason() { return props.accountIds.length > 100 ? t('admin.plugins.accountLimit') : t('admin.plugins.extensionUnavailable') }
function matches(item: PluginContribution, account: AccountSelectionIdentity) {
  const filter = item.account_filter
  return !filter || ((!filter.platforms?.length || filter.platforms.includes(account.platform)) &&
    (!filter.types?.length || filter.types.includes(account.type)) &&
    (!filter.statuses?.length || filter.statuses.includes(account.status || '')) &&
    (!filter.exclude_shadows || account.parent_account_id == null))
}
const visible = computed(() => registry.items.filter(item => {
  if (item.slot !== props.name || item.permission !== 'admin') return false
  if (props.account) return matches(item, props.account)
  if (props.accountIds.length) {
    const known = new Map(props.accounts.map(account => [account.id, account]))
    return props.accountIds.every(id => { const account = known.get(id); return !!account && matches(item, account) })
  }
  return !item.account_filter
}))
watch(visible, items => {
  if (selected.value && !activeJob.value && !items.some(item => item.plugin_id === selected.value?.plugin_id && item.id === selected.value?.id)) selected.value = null
})
onMounted(() => { if (!registry.loaded) void registry.refresh() })
</script>
