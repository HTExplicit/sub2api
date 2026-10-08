<template>
  <label v-if="levels.length || modelValue" class="mt-3 block text-sm">
    {{ t('admin.accounts.testReasoning.label') }}
    <select :value="modelValue" class="input mt-1 w-full" :disabled="disabled" :aria-invalid="!valid" @change="emit('update:modelValue', ($event.target as HTMLSelectElement).value)">
      <option v-if="modelValue && !valid && !isUltra" :value="modelValue" disabled>{{ modelValue }}</option>
      <option value="">{{ t('admin.accounts.testReasoning.default') }}{{ defaultEffort ? ` (${defaultEffort})` : '' }}</option>
      <option v-for="level in levels" :key="level" :value="level">{{ level }}</option>
    </select>
  </label>
</template>
<script setup lang="ts">
import { computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AccountAvailableModel } from '@/types'
import { accountTestReasoningLevels, isAccountTestReasoningValid } from '@/utils/accountTestModels'
const props = defineProps<{ modelValue: string; model?: AccountAvailableModel; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const { t } = useI18n()
const levels = computed(() => accountTestReasoningLevels(props.model))
const isUltra = computed(() => props.modelValue.trim().toLowerCase() === 'ultra')
const defaultEffort = computed(() => props.model?.default_reasoning_effort?.trim().toLowerCase() === 'ultra' ? '' : props.model?.default_reasoning_effort)
const valid = computed(() => isAccountTestReasoningValid(props.model, props.modelValue))
watch(isUltra, value => { if (value) emit('update:modelValue', '') }, { immediate: true })
</script>
