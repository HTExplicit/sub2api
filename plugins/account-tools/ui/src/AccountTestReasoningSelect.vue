<template>
  <label v-if="levels.length" class="mt-3 block text-sm">
    {{ t('tools.reasoningLabel') }}
    <select :value="modelValue" class="input mt-1 w-full" :disabled="disabled" @change="emit('update:modelValue', ($event.target as HTMLSelectElement).value)">
      <option value="">{{ t('tools.reasoningDefault') }}{{ model?.default_reasoning_effort ? ` (${model.default_reasoning_effort})` : '' }}</option>
      <option v-for="level in levels" :key="level" :value="level">{{ level }}</option>
    </select>
  </label>
</template>
<script setup lang="ts">
import { computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AccountAvailableModel } from './api'
const props = defineProps<{ modelValue: string; model?: AccountAvailableModel; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: string]; validity: [valid: boolean] }>()
const { t } = useI18n()
const levels = computed(() => props.model?.reasoning_efforts || [])
watch(() => [props.modelValue, levels.value] as const, () => {
  if (!levels.value.length && props.modelValue) emit('update:modelValue', '')
  emit('validity', !props.modelValue || levels.value.includes(props.modelValue) || !levels.value.length)
}, { immediate: true })
</script>
