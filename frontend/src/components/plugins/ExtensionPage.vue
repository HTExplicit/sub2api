<template>
  <ExtensionSurface :name="name" :plugin-key="contribution?.plugin_key" :plugin-id="contribution?.plugin_id" :package-sha="contribution?.package_sha256">
    <PluginFrame v-if="contribution" :plugin-id="contribution.plugin_id" :title="title"
      :permission="contribution.permission === 'user' ? 'user' : 'admin'"
      :expected-package="contribution.package_sha256"
      :context="{ contribution_id: contribution.id, mode: 'page' }" />
  </ExtensionSurface>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import ExtensionSurface from './ExtensionSurface.vue'
import PluginFrame from './PluginFrame.vue'
import { useRetainedContribution } from './useRetainedContribution'

const props = defineProps<{ name: string }>()
const { locale } = useI18n()
const { contribution } = useRetainedContribution(() => ({ id: props.name, slot: 'surface' }))
const title = computed(() => contribution.value?.label[String(locale.value).startsWith('zh') ? 'zh' : 'en'] || props.name)
</script>
