<template>
  <div class="space-y-4 border-t border-gray-200 pt-4 dark:border-dark-600" data-testid="openai-reasoning-policy">
    <div v-for="field in fields" :key="field.key" class="flex items-start justify-between gap-4">
      <div class="min-w-0 flex-1">
        <label :for="`${idPrefix}-${field.key}-toggle`" class="input-label mb-0">
          {{ t(`admin.accounts.openai.reasoningPolicy.${field.key}`) }}
        </label>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
          {{ t(`admin.accounts.openai.reasoningPolicy.${field.key}Desc`) }}
        </p>
        <label v-if="bulk" class="mt-2 flex w-fit cursor-pointer items-center gap-2 text-xs text-gray-600 dark:text-gray-300">
          <input
            type="checkbox"
            :data-testid="`openai-reasoning-${field.key}-selected`"
            :checked="selected[field.key]"
            class="rounded border-gray-300 text-primary-600 focus:ring-primary-500"
            @change="selectField(field.key, $event)"
          />
          {{ t('admin.accounts.openai.reasoningPolicy.applyField') }}
        </label>
      </div>
      <Toggle
        :id="`${idPrefix}-${field.key}-toggle`"
        :data-testid="`openai-reasoning-${field.key}-toggle`"
        :model-value="modelValue[field.key]"
        :disabled="bulk && !selected[field.key]"
        :aria-label="t(`admin.accounts.openai.reasoningPolicy.${field.key}`)"
        :class="bulk && !selected[field.key] ? 'cursor-not-allowed opacity-50' : ''"
        @update:model-value="updateField(field.key, $event)"
      />
    </div>
    <p class="rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:bg-amber-900/20 dark:text-amber-300">
      {{ t('admin.accounts.openai.reasoningPolicy.boundaryHint') }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
import type { OpenAIReasoningPolicy, OpenAIReasoningPolicyField } from '@/utils/openaiReasoningPolicy'

const props = withDefaults(defineProps<{
  modelValue: OpenAIReasoningPolicy
  selected: OpenAIReasoningPolicy
  idPrefix: string
  bulk?: boolean
}>(), { bulk: false })

const emit = defineEmits<{
  'update:modelValue': [value: OpenAIReasoningPolicy]
  'update:selected': [value: OpenAIReasoningPolicy]
}>()

const { t } = useI18n()
const fields: Array<{ key: OpenAIReasoningPolicyField }> = [
  { key: 'chatReplay' },
  { key: 'signatureRecovery' }
]

function updateField(field: OpenAIReasoningPolicyField, value: boolean) {
  if (props.bulk && !props.selected[field]) return
  emit('update:modelValue', { ...props.modelValue, [field]: value })
  if (!props.bulk) emit('update:selected', { ...props.selected, [field]: true })
}

function selectField(field: OpenAIReasoningPolicyField, event: Event) {
  const checked = (event.target as HTMLInputElement).checked
  emit('update:selected', { ...props.selected, [field]: checked })
}
</script>
