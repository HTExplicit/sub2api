<template>
  <ExtensionSurface :name="name">
    <PluginFrame v-if="contribution" :plugin-id="contribution.plugin_id" :title="title" inline
      :context="{ ...context, contribution_id: contribution.id, mode: 'widget' }"
      @event="(event, payload) => emit('event', event, payload)" @job="job => emit('job', job)" />
  </ExtensionSurface>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { usePluginExtensions } from '@/stores/pluginExtensions'
import type { AccountJob } from '@/api/admin/accountJobs'
import ExtensionSurface from './ExtensionSurface.vue'
import PluginFrame from './PluginFrame.vue'
const props = withDefaults(defineProps<{ name: string; context?: Record<string, unknown> }>(), { context: () => ({}) })
const emit = defineEmits<{ event: [name: string, payload: unknown]; job: [job: AccountJob] }>()
const registry = usePluginExtensions()
const { locale } = useI18n()
const contribution = computed(() => registry.items.find(item => item.slot === 'surface' && item.id === props.name))
const title = computed(() => contribution.value?.label[String(locale.value).startsWith('zh') ? 'zh' : 'en'] || props.name)
</script>
