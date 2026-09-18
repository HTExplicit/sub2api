<template>
  <div class="mb-3 space-y-1.5">
    <label :for="id" class="text-sm font-medium">{{ t('admin.accounts.textTestPrompt.label') }}</label>
    <textarea :id="id" :value="modelValue" class="input w-full" rows="3" placeholder="hi" :disabled="disabled"
      @input="emit('update:modelValue', ($event.target as HTMLTextAreaElement).value)" />
    <div class="flex justify-between gap-3 text-xs text-gray-500">
      <span>{{ t('admin.accounts.textTestPrompt.hint') }} {{ length }}/8192</span>
      <button type="button" :disabled="disabled" @click="emit('update:modelValue', '')">{{ t('admin.accounts.textTestPrompt.reset') }}</button>
    </div>
    <p v-if="length > 8192" role="alert" class="text-sm text-red-600">{{ t('admin.accounts.textTestPrompt.tooLong') }}</p>
  </div>
</template>
<script setup lang="ts">
import { computed, useId } from 'vue'
import { useI18n } from 'vue-i18n'
const props = defineProps<{ modelValue: string; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const { t } = useI18n()
const id = useId()
const length = computed(() => Array.from(props.modelValue).length)
</script>
