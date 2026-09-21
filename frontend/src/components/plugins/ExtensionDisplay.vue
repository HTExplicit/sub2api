<template>
  <div v-if="contribution" class="min-w-0 text-xs" :data-extension-display="name" :title="contribution.available ? undefined : t('admin.plugins.extensionUnavailable')">
    <div v-if="showLabel" class="mb-1 text-[10px] font-medium uppercase text-gray-400">{{ label(contribution.label) }}</div>
    <div v-if="hasValues" class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
      <span v-for="field in contribution.display_fields" :key="field.key" :data-display-key="field.key" :class="[
        field.new_line ? 'basis-full text-[11px]' : '', field.kind === 'badge' ? 'max-w-full break-words rounded-none px-1.5 py-0.5 text-[10px] font-medium leading-4' : 'font-mono text-gray-500 dark:text-dark-300',
        field.kind === 'badge' ? toneClass(field) : ''
      ]">{{ display(field) }}</span>
    </div>
    <span v-else class="text-muted" data-display-empty>--</span>
  </div>
</template>
<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PluginDisplayField } from '@/api/admin/plugins'
import { usePluginExtensions } from '@/stores/pluginExtensions'
import { formatDateTime } from '@/utils/format'
import { useRetainedContribution } from './useRetainedContribution'
const props = defineProps<{ name: string; values: Record<string, unknown>; showLabel?: boolean; pluginKey?: string; pluginId?: number; packageSha?: string }>()
const registry = usePluginExtensions()
const { t, locale } = useI18n()
const { contribution } = useRetainedContribution(() => ({ id: props.name, slot: 'account.columns', pluginKey: props.pluginKey, pluginId: props.pluginId, packageSHA: props.packageSha }))
function value(field: PluginDisplayField) {
  if (!Object.prototype.hasOwnProperty.call(props.values, field.key)) return ''
  const value = props.values[field.key]
  return typeof value === 'string' || (typeof value === 'number' && Number.isFinite(value)) ? String(value).trim() : ''
}
const hasValues = computed(() => contribution.value?.display_fields?.some(field => value(field) !== ''))
function label(labels: Record<string, string>) { return labels[locale?.value || 'zh'] || labels[String(locale?.value).startsWith('zh') ? 'zh' : 'en'] || labels.zh || labels.en || '' }
function choice(field: PluginDisplayField) { const key = value(field); return field.values && Object.prototype.hasOwnProperty.call(field.values, key) ? field.values[key] : undefined }
function display(field: PluginDisplayField) {
  const text = value(field)
  if (!text) return '--'
  if (field.kind === 'datetime') return formatDateTime(text) || '--'
  return (field.prefix || '') + (choice(field) ? label(choice(field)!.label) : text)
}
function toneClass(field: PluginDisplayField) {
  const tones = {
    neutral: 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-dark-300',
    success: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-300',
    warning: 'bg-amber-50 text-amber-700 dark:bg-amber-900/20 dark:text-amber-300',
    danger: 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-300',
    info: 'bg-primary-50 text-primary-700 dark:bg-primary-900/20 dark:text-primary-300'
  }
  return tones[choice(field)?.tone || 'neutral'] || tones.neutral
}
onMounted(() => { if (!registry.loaded) void registry.refresh() })
</script>
