<template>
  <AccountOperationDialog :show="contribution !== null" :title="title" :job="activeJob" :show-configuration="false" width="normal" @close="emit('close')">
    <PluginFrame v-if="contribution && !activeJob" :plugin-id="contribution.plugin_id" :title="title"
      :context="{ contribution_id: contribution.id, operation: contribution.action, account_id: accountId, account_ids: accountIds, mode }"
      @job="acceptJob" @saved="registry.refresh" />
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
const props = withDefaults(defineProps<{ contribution: PluginContribution | null; accountId?: number; accountIds?: number[]; mode?: string }>(), { accountIds: () => [], mode: 'account.actions' })
const emit = defineEmits<{ close: []; job: [job: AccountJob] }>()
const { locale } = useI18n()
const registry = usePluginExtensions()
const activeJob = ref<AccountJob | null>(null)
const title = computed(() => props.contribution?.label[locale?.value || 'zh'] || props.contribution?.label.zh || props.contribution?.label.en || '')
watch(() => [props.contribution?.plugin_id, props.contribution?.id, props.accountId, props.accountIds.join(',')], () => { activeJob.value = null })
function acceptJob(job: AccountJob) { activeJob.value = job; emit('job', job) }
</script>
