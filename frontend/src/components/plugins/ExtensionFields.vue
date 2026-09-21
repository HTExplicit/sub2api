<template>
  <div v-for="entry in displayed" :key="entry.identity" class="mt-3">
    <label class="flex flex-col gap-1.5 text-sm font-medium">
      {{ label(entry.field.label) }}
      <Select v-if="entry.field.kind === 'select'" :model-value="values[entry.field.key] || ''" :options="options(entry.field)" :disabled="disabled || !entry.available"
        @update:model-value="value => update(entry.field.key, typeof value === 'string' ? value : '')" />
      <textarea v-else class="input w-full" :rows="entry.field.rows" :placeholder="entry.field.placeholder" :value="values[entry.field.key] || ''" :disabled="disabled || !entry.available"
        @input="update(entry.field.key, ($event.target as HTMLTextAreaElement).value)" />
    </label>
    <div v-if="entry.field.kind === 'textarea'" class="mt-1 flex justify-between gap-3 text-xs text-muted">
      <span>{{ label(entry.field.hint || {}) }} {{ textLength(entry.field) }}/{{ entry.field.max_length }}</span>
      <button v-if="entry.field.reset_label" type="button" :disabled="disabled" @click="update(entry.field.key, '')">{{ label(entry.field.reset_label) }}</button>
    </div>
    <p v-if="entry.field.kind === 'textarea' && textLength(entry.field) > (entry.field.max_length || 0)" role="alert" class="text-sm text-red-600">{{ label(entry.field.limit_message || {}) }}</p>
    <p v-if="!entry.available" role="status" class="mt-1 text-xs text-muted">{{ t('admin.plugins.extensionUnavailable') }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import type { PluginFormField } from '@/api/admin/plugins'
import type { AccountSelectionIdentity } from '@/composables/useAccountSelectionMetadata'
import { usePluginExtensions } from '@/stores/pluginExtensions'
import { contributionAdmission } from './contributionAdmission'

const props = withDefaults(defineProps<{ name: string; values: Record<string, string>; context: Record<string, unknown>; account?: AccountSelectionIdentity | null; disabled?: boolean }>(), { disabled: false })
const emit = defineEmits<{ 'update:values': [values: Record<string, string>]; validity: [valid: boolean] }>()
const { t, locale } = useI18n()
const registry = usePluginExtensions()
const declared = computed(() => registry.items.filter(item => item.slot === props.name && item.permission === 'admin').flatMap(item => (item.fields || []).map(field => ({ field, available: contributionAdmission(item, { account: props.account }).allowed, identity: `${item.plugin_id}:${item.id}:${field.key}` }))))
function label(labels: Record<string, string>) { return labels[locale?.value || 'zh'] || labels[(locale?.value || 'zh').split('-')[0]] || labels.zh || labels.en || '' }
function levels(field: PluginFormField) { const source = field.options_source; const value = source && Object.prototype.hasOwnProperty.call(props.context, source) ? props.context[source] : null; return Array.isArray(value) ? [...new Set(value.filter((item): item is string => typeof item === 'string'))] : [] }
function options(field: PluginFormField) {
  const defaultValue = field.default_source && Object.prototype.hasOwnProperty.call(props.context, field.default_source) ? props.context[field.default_source] : null
  const defaultLabel = label(field.default_label || {}) + (typeof defaultValue === 'string' && defaultValue ? ` (${defaultValue})` : '')
  return [{ value: '', label: defaultLabel }, ...levels(field).map(value => ({ value, label: value }))]
}
function textLength(field: PluginFormField) { return Array.from(props.values[field.key] || '').length }
const displayed = computed(() => declared.value.filter(entry => entry.field.kind === 'textarea' || levels(entry.field).length > 0))
function update(key: string, value: string) { emit('update:values', { ...props.values, [key]: value }) }
watch([declared, () => props.context, () => props.values], () => {
  // Eligibility is not ownership of the user's draft. Scope changes, missing
  // declarations and model switches must not silently clear stored input.
  emit('validity', declared.value.every(entry => (!props.values[entry.field.key] || entry.available) &&
    (entry.field.kind !== 'textarea' || textLength(entry.field) <= (entry.field.max_length || 0))))
}, { immediate: true, deep: true })
onMounted(() => { if (!registry.loaded) void registry.refresh() })
</script>
