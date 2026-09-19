<template>
  <div v-for="entry in displayed" :key="entry.identity" class="mt-3">
    <label class="flex flex-col gap-1.5 text-sm font-medium">
      {{ label(entry.field.label) }}
      <Select :model-value="values[entry.field.key] || ''" :options="options(entry.field)" :disabled="disabled || !entry.available"
        @update:model-value="value => update(entry.field.key, typeof value === 'string' ? value : '')" />
    </label>
    <p v-if="!entry.available" role="status" class="mt-1 text-xs text-muted">{{ t('admin.plugins.extensionUnavailable') }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import type { PluginFormField } from '@/api/admin/plugins'
import { usePluginExtensions } from '@/stores/pluginExtensions'

const props = withDefaults(defineProps<{ name: string; values: Record<string, string>; context: Record<string, unknown>; disabled?: boolean }>(), { disabled: false })
const emit = defineEmits<{ 'update:values': [values: Record<string, string>]; validity: [valid: boolean] }>()
const { t, locale } = useI18n()
const registry = usePluginExtensions()
const managed = new Set<string>()
const declared = computed(() => registry.items.filter(item => item.slot === props.name && item.permission === 'admin').flatMap(item => (item.fields || []).map(field => ({ field, available: item.available, identity: `${item.plugin_id}:${item.id}:${field.key}` }))))
function label(labels: Record<string, string>) { return labels[locale?.value || 'zh'] || labels[(locale?.value || 'zh').split('-')[0]] || labels.zh || labels.en || '' }
function levels(field: PluginFormField) { const value = Object.prototype.hasOwnProperty.call(props.context, field.options_source) ? props.context[field.options_source] : null; return Array.isArray(value) ? [...new Set(value.filter((item): item is string => typeof item === 'string'))] : [] }
function options(field: PluginFormField) {
  const defaultValue = field.default_source && Object.prototype.hasOwnProperty.call(props.context, field.default_source) ? props.context[field.default_source] : null
  const defaultLabel = label(field.default_label) + (typeof defaultValue === 'string' && defaultValue ? ` (${defaultValue})` : '')
  return [{ value: '', label: defaultLabel }, ...levels(field).map(value => ({ value, label: value }))]
}
const displayed = computed(() => declared.value.filter(entry => levels(entry.field).length > 0))
function update(key: string, value: string) { emit('update:values', { ...props.values, [key]: value }) }
watch([declared, () => props.context, () => props.values], () => {
  const fields = new Map(declared.value.map(entry => [entry.field.key, entry]))
  for (const key of fields.keys()) managed.add(key)
  const values = { ...props.values }
  let changed = false
  for (const key of managed) {
    const entry = fields.get(key)
    if (values[key] && (!entry || !levels(entry.field).includes(values[key]))) { values[key] = ''; changed = true }
  }
  if (changed) emit('update:values', values)
  emit('validity', declared.value.every(entry => !values[entry.field.key] || entry.available))
}, { immediate: true, deep: true })
onMounted(() => { if (!registry.loaded) void registry.refresh() })
</script>
