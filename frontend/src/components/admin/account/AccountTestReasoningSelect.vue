<template>
  <label v-if="levels.length" class="mt-3 flex flex-col gap-1.5 text-sm font-medium">
    {{ t('admin.accounts.testReasoning.label') }}
    <Select :model-value="modelValue" :options="options" :disabled="disabled"
      @update:model-value="value => emit('update:modelValue', typeof value === 'string' ? value : '')" />
  </label>
</template>

<script setup lang="ts">
import { computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import type { AccountAvailableModel } from '@/types'

const props = defineProps<{ modelValue: string; model?: AccountAvailableModel; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const { t } = useI18n()
const levels = computed(() => [...new Set(props.model?.reasoning_efforts || [])])
const options = computed(() => [
  { value: '', label: t('admin.accounts.testReasoning.default') },
  ...levels.value.map(value => ({ value, label: value }))
])
watch([() => props.model?.id, levels], () => {
  if (props.modelValue && !levels.value.includes(props.modelValue)) emit('update:modelValue', '')
}, { immediate: true })
</script>
