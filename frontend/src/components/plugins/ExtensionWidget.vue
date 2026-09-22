<template>
  <ExtensionSurface :name="name" :plugin-key="contribution?.plugin_key || pluginKey" :plugin-id="contribution?.plugin_id || pluginId" :package-sha="contribution?.package_sha256 || packageSha">
    <PluginFrame v-if="contribution" :plugin-id="contribution.plugin_id" :title="title" inline
      :expected-package="contribution.package_sha256" :origin-view="originView"
      :context="{ ...context, contribution_id: contribution.id, mode: 'widget' }"
      @event="(event, payload) => emit('event', event, payload)" @job="job => emit('job', job)" />
  </ExtensionSurface>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AccountJob } from '@/api/admin/accountJobs'
import ExtensionSurface from './ExtensionSurface.vue'
import PluginFrame from './PluginFrame.vue'
import type { CapturedAccountView } from '@/composables/useAccountViewContext'
import { useRetainedContribution } from './useRetainedContribution'
const props = withDefaults(defineProps<{ name: string; context?: Record<string, unknown>; pluginKey?: string; pluginId?: number; packageSha?: string; originView?: CapturedAccountView | null }>(), { context: () => ({}) })
const emit = defineEmits<{ event: [name: string, payload: unknown]; job: [job: AccountJob] }>()
const { locale } = useI18n()
const { contribution } = useRetainedContribution(() => ({ id: props.name, slot: 'surface', pluginKey: props.pluginKey, pluginId: props.pluginId, packageSHA: props.packageSha }))
const title = computed(() => contribution.value?.label[String(locale.value).startsWith('zh') ? 'zh' : 'en'] || props.name)
</script>
