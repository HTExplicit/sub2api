<template>
  <AccountsView v-if="view" :view-contribution="view" />
  <AppLayout v-else>
    <p role="status" class="rounded border border-line bg-raised p-4 text-sm text-muted" data-test="account-view-unavailable">
      {{ registry.loaded ? t('admin.plugins.extensionUnavailable') : t('common.loading') }}
    </p>
  </AppLayout>
</template>
<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import AccountsView from '@/views/admin/AccountsView.vue'
import { usePluginExtensions } from '@/stores/pluginExtensions'
import { isAccountView } from './accountView'
import { useRetainedContribution } from './useRetainedContribution'

const props = defineProps<{ pluginKey: string; viewId: string }>()
const { t } = useI18n()
const registry = usePluginExtensions()
const { contribution } = useRetainedContribution(() => ({ pluginKey: props.pluginKey, id: props.viewId, slot: 'account.view.v1' }))
const view = computed(() => isAccountView(contribution.value) ? contribution.value : undefined)
onMounted(() => { if (!registry.loaded) void registry.refresh() })
</script>
