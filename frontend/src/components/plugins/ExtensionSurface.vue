<template>
  <div v-if="contribution" class="contents">
    <div class="contents" :inert="!contribution.retained_controls && !contribution.available ? true : undefined" :aria-disabled="!contribution.retained_controls && !contribution.available">
      <slot :available="contribution.available" />
    </div>
    <p v-if="!contribution.available" role="status" class="px-3 py-2 text-xs text-muted">{{ t('admin.plugins.extensionUnavailable') }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, provide } from 'vue'
import { useI18n } from 'vue-i18n'
import { usePluginExtensions } from '@/stores/pluginExtensions'
import { extensionAvailabilityKey, extensionUnavailableMessageKey } from './context'
import { useRetainedContribution } from './useRetainedContribution'
const props = defineProps<{ name: string; pluginKey?: string; pluginId?: number; packageSha?: string }>()
const registry = usePluginExtensions()
const { t } = useI18n()
const { contribution } = useRetainedContribution(() => ({ id: props.name, slot: 'surface', pluginKey: props.pluginKey, pluginId: props.pluginId, packageSHA: props.packageSha }))
provide(extensionAvailabilityKey, computed(() => !!contribution.value?.available))
provide(extensionUnavailableMessageKey, computed(() => t('admin.plugins.extensionUnavailable')))
onMounted(() => { if (!registry.loaded) void registry.refresh() })
</script>
