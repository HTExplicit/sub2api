<template>
  <div class="mb-3 space-y-1.5">
    <label :for="id" class="text-sm font-medium">{{ t('admin.accounts.textTestPrompt.label') }}</label>
    <textarea :id="id" :value="modelValue" class="input w-full" rows="3" placeholder="hi" :disabled="disabled"
      @input="emit('update:modelValue', ($event.target as HTMLTextAreaElement).value)" />
    <div class="flex justify-between gap-3 text-xs text-gray-500">
      <span>{{ t('admin.accounts.textTestPrompt.hint') }} {{ length }}/{{ ACCOUNT_TEST_PROMPT_LIMIT }}</span>
      <button type="button" :disabled="disabled" @click="emit('update:modelValue', '')">{{ t('admin.accounts.textTestPrompt.reset') }}</button>
    </div>
    <p v-if="!valid" role="alert" class="text-sm text-red-600">{{ t('admin.accounts.textTestPrompt.tooLong') }}</p>
  </div>
</template>
<script setup lang="ts">
import { computed, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AccountSelectionIdentity } from '@/composables/useAccountSelectionMetadata'
import { ACCOUNT_TEST_PROMPT_LIMIT } from '@/composables/useAccountTestPrompt'
const props = defineProps<{ modelValue: string; account?: AccountSelectionIdentity | null; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: string]; validity: [valid: boolean] }>()
const { t } = useI18n()
const id = useId()
const length = computed(() => Array.from(props.modelValue).length)
const valid = computed(() => length.value <= ACCOUNT_TEST_PROMPT_LIMIT)
watch(valid, value => emit('validity', value), { immediate: true })
</script>
