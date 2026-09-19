<template>
  <div v-if="contribution" class="contents">
    <div class="contents" :inert="!contribution.available" :aria-disabled="!contribution.available">
      <slot />
    </div>
    <p v-if="!contribution.available" role="status" class="px-3 py-2 text-xs text-muted">{{ t('admin.plugins.extensionUnavailable') }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, provide } from 'vue'
import { useI18n } from 'vue-i18n'
import { usePluginExtensions } from '@/stores/pluginExtensions'
import { extensionAvailabilityKey, extensionUnavailableMessageKey } from './context'
const props = defineProps<{ name: string }>()
const registry = usePluginExtensions()
const { t } = useI18n()
const contribution = computed(() => registry.items.find(item => item.slot === 'surface' && item.id === props.name))
provide(extensionAvailabilityKey, computed(() => !!contribution.value?.available))
provide(extensionUnavailableMessageKey, computed(() => t('admin.plugins.extensionUnavailable')))
onMounted(() => { if (!registry.loaded) void registry.refresh() })
</script>
