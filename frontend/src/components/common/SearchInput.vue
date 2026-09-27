<template>
  <div class="relative w-full">
    <div class="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3">
      <Icon name="search" :size="flatThemeActive ? 'sm' : 'md'" class="text-gray-400" />
    </div>
    <input
      v-model="searchValue"
      type="text"
      :class="['input', flatThemeActive ? 'pl-9' : 'pl-10']"
      :placeholder="placeholder"
    />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useDebounceFn } from '@vueuse/core'
import Icon from '@/components/icons/Icon.vue'
// Console theme: a 16px icon and a tighter inset; with the theme off the upstream 20px icon and pl-10.
import { flatThemeActive } from '@/utils/flatTheme'

const props = withDefaults(defineProps<{
  modelValue: string
  placeholder?: string
  debounceMs?: number
}>(), {
  placeholder: 'Search...',
  debounceMs: 300
})

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
  (e: 'search', value: string): void
}>()

const debouncedEmitSearch = useDebounceFn((value: string) => {
  emit('search', value)
}, props.debounceMs)

const searchValue = computed({
  get: () => props.modelValue,
  set: (value: string) => {
    emit('update:modelValue', value)
    debouncedEmitSearch(value)
  }
})
</script>
