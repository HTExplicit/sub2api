<template>
  <div
    class="inline-flex h-9 items-center rounded-none border border-line bg-raised p-0.5"
    role="group"
    :aria-label="t('admin.accounts.viewModeLabel')"
  >
    <button
      v-for="option in options"
      :key="option.value"
      type="button"
      class="inline-flex h-7 w-8 items-center justify-center rounded-none transition-colors"
      :class="modelValue === option.value
        ? 'border-b-2 border-primary-500 text-primary-700 dark:text-primary-300'
        : 'text-muted hover:text-gray-700 dark:hover:text-gray-200'"
      :title="option.label"
      :aria-label="option.label"
      :aria-pressed="modelValue === option.value"
      :data-test="`account-view-${option.value}`"
      @click="emit('update:modelValue', option.value)"
    >
      <Icon :name="option.icon" size="sm" />
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'

export type AccountViewMode = 'table' | 'compact' | 'cards'

defineProps<{ modelValue: AccountViewMode }>()

const emit = defineEmits<{
  'update:modelValue': [value: AccountViewMode]
}>()

const { t } = useI18n()
const options = computed(() => [
  { value: 'table' as const, icon: 'sort' as const, label: t('admin.accounts.viewModeTable') },
  { value: 'compact' as const, icon: 'menu' as const, label: t('admin.accounts.viewModeCompact') },
  { value: 'cards' as const, icon: 'grid' as const, label: t('admin.accounts.viewModeCards') }
])
</script>
